#!/usr/bin/env bash
# Shebang: Specifies that the script should be executed with bash (Bourne-Again SHell).

# ==============================================================================
# Plist Generator / Manager Script
# Description: A user-friendly wrapper around macOS 'plutil' that simplifies
#              common property list operations: creating, reading, converting,
#              validating, and editing plist files.
#              Provides an interactive menu if run without arguments.
#
# Usage:       ./plistgenerator.sh <command> [options]
#           OR ./plistgenerator.sh (for interactive menu)
#              (Make the script executable first: chmod +x plistgenerator.sh)
#
# Commands:
#   create              Create a new empty plist file (xml1 format)
#   read                Print plist contents in human-readable form
#   convert             Convert a plist to another format (xml1, binary1, json, swift, objc)
#   lint                Validate plist syntax
#   insert              Insert a key-value pair into a plist
#   replace             Replace an existing key-value in a plist
#   remove              Remove a key from a plist
#   extract             Extract and print a value at a keypath
#   type                Print the type of a value at a keypath
#
# Common Options:
#   -f, --file <path>   Target plist file (required for most commands)
#   -k, --key <keypath> Key path for insert/replace/remove/extract/type
#   -t, --type <type>   Value type: bool, integer, float, string, date, data,
#                        xml, json, array, dictionary
#   -v, --value <val>   Value to set (for insert/replace)
#   -F, --format <fmt>  Conversion target format: xml1, binary1, json, swift, objc
#   -o, --output <path> Output file path (for convert; defaults to in-place)
#   -a, --append        Append to array (with insert)
#   -s, --silent        Suppress success messages
#   -r, --readable      Pretty-print JSON output
#   -h, --help          Display help (global or per-command)
#
# Requires:    macOS with plutil available (ships with macOS).
#              Compatible with Bash 3.2+
# ==============================================================================

# --- Script Setup ---
# Note: 'set -e' is intentionally omitted so that the interactive menu loop
# survives individual command failures gracefully. Errors are handled explicitly.
set -uo pipefail

# Flag to track whether we are inside the interactive menu loop.
IN_MENU_LOOP=false

SCRIPT_NAME="$(basename "$0")"
PLUTIL_CMD="/usr/bin/plutil"

# --- Color & Formatting ---
if [[ -t 1 ]]; then
    C_RESET='\033[0m'
    C_BOLD='\033[1m'
    C_RED='\033[0;31m'
    C_GREEN='\033[0;32m'
    C_YELLOW='\033[0;33m'
    C_CYAN='\033[0;36m'
    C_DIM='\033[2m'
else
    C_RESET='' C_BOLD='' C_RED='' C_GREEN='' C_YELLOW='' C_CYAN='' C_DIM=''
fi

# --- Utility Functions ---

# Function: print_msg
# Purpose: Print a formatted message to stderr.
# Arguments: $1 = level (info|success|warn|error), $2 = message
print_msg() {
    local level="$1" msg="$2"
    case "$level" in
        info)    echo -e "${C_CYAN}[INFO]${C_RESET} $msg" >&2 ;;
        success) echo -e "${C_GREEN}[OK]${C_RESET} $msg" >&2 ;;
        warn)    echo -e "${C_YELLOW}[WARN]${C_RESET} $msg" >&2 ;;
        error)   echo -e "${C_RED}[ERROR]${C_RESET} $msg" >&2 ;;
    esac
}

# Function: die
# Purpose: Print an error message and exit (or return if inside the menu loop).
# Arguments: $1 = message, $2 = exit code (default 1)
die() {
    print_msg error "$1"
    if $IN_MENU_LOOP; then
        # Return 2 to distinguish from the quit signal (1)
        return 2
    fi
    exit "${2:-1}"
}

# Function: require_file
# Purpose: Ensure a file argument was provided and the file exists.
# Arguments: $1 = file path (may be empty)
require_file() {
    if [[ -z "${1:-}" ]]; then
        die "No file specified. Use -f <path> or --file <path>."
        return $?
    fi
    if [[ ! -f "$1" ]]; then
        die "File not found: $1"
        return $?
    fi
    return 0
}

# Function: require_keypath
# Purpose: Ensure a keypath argument was provided.
# Arguments: $1 = keypath (may be empty)
require_keypath() {
    if [[ -z "${1:-}" ]]; then
        die "No keypath specified. Use -k <keypath> or --key <keypath>."
        return $?
    fi
    return 0
}

# Function: require_plutil
# Purpose: Ensure plutil is available on the system.
require_plutil() {
    if ! command -v "$PLUTIL_CMD" &>/dev/null; then
        die "'plutil' not found. This script requires macOS with plutil installed."
        return $?
    fi
    return 0
}

# Function: resolve_path
# Purpose: Resolve a potentially relative path to an absolute path.
# Arguments: $1 = path
resolve_path() {
    local p="$1"
    if [[ "$p" != /* ]]; then
        p="$(pwd)/$p"
    fi
    echo "$p"
}

# --- Supported Formats / Types ---

# Conversion target formats supported by plutil.
VALID_CONVERT_FORMATS="xml1 binary1 json swift objc"
VALID_TYPES="bool integer float string date data xml json array dictionary"

# Function: validate_format
# Purpose: Check that a format string is valid for plutil.
# Arguments: $1 = format string
validate_convert_format() {
    local fmt="$1"
    local f
    for f in $VALID_CONVERT_FORMATS; do
        [[ "$f" == "$fmt" ]] && return 0
    done
    die "Invalid format '$fmt'. Valid formats: $VALID_CONVERT_FORMATS"
    return $?
}

# Function: validate_type
# Purpose: Check that a type string is valid for plutil.
# Arguments: $1 = type string
validate_type() {
    local t="$1"
    local v
    for v in $VALID_TYPES; do
        [[ "$v" == "$t" ]] && return 0
    done
    die "Invalid type '$t'. Valid types: $VALID_TYPES"
    return $?
}

# ==========================================================================
# Command: usage / help
# ==========================================================================

usage() {
    cat <<EOF
${C_BOLD}$SCRIPT_NAME${C_RESET} — a friendly wrapper around macOS plutil

${C_BOLD}USAGE${C_RESET}
  $SCRIPT_NAME <command> [options]
  $SCRIPT_NAME                        (interactive menu)

${C_BOLD}COMMANDS${C_RESET}
  ${C_CYAN}create${C_RESET}    Create a new empty plist file (xml1)
  ${C_CYAN}import${C_RESET}    Import a JSON file as a plist
  ${C_CYAN}read${C_RESET}      Print plist in human-readable form
  ${C_CYAN}convert${C_RESET}   Convert a plist to another format
  ${C_CYAN}lint${C_RESET}      Validate plist syntax
  ${C_CYAN}insert${C_RESET}    Insert a key-value pair
  ${C_CYAN}replace${C_RESET}   Replace an existing key-value
  ${C_CYAN}remove${C_RESET}    Remove a key
  ${C_CYAN}extract${C_RESET}   Extract a value at a keypath
  ${C_CYAN}type${C_RESET}      Show the type of a value at a keypath

${C_BOLD}COMMON OPTIONS${C_RESET}
  -f, --file <path>     Target plist file
  -k, --key <keypath>   Key path (dot-separated, e.g. "CFBundleName" or "items.0.name")
  -t, --type <type>     Value type: ${C_DIM}$VALID_TYPES${C_RESET}
  -v, --value <val>     Value to set
  -F, --format <fmt>    Conversion format: ${C_DIM}$VALID_CONVERT_FORMATS${C_RESET}
  -o, --output <path>   Output file (convert only; default: in-place)
  -a, --append          Append to array (insert only)
  -s, --silent          Suppress success messages
  -r, --readable        Pretty-print JSON output
  -h, --help            Show help (global or per-command)

${C_BOLD}EXAMPLES${C_RESET}
  $SCRIPT_NAME create  -f config.plist
  $SCRIPT_NAME import  -f data.json -o config.plist
  $SCRIPT_NAME read    -f config.plist
  $SCRIPT_NAME convert -f config.plist -F json -o config.json
  $SCRIPT_NAME lint   -f config.plist
  $SCRIPT_NAME insert  -f config.plist -k CFBundleName -t string -v "MyApp"
  $SCRIPT_NAME insert  -f config.plist -k tags -t array
  $SCRIPT_NAME insert  -f config.plist -k tags -t string -v "beta" -a
  $SCRIPT_NAME replace -f config.plist -k CFBundleName -t string -v "NewName"
  $SCRIPT_NAME remove  -f config.plist -k CFBundleName
  $SCRIPT_NAME extract -f config.plist -k CFBundleName -F raw
  $SCRIPT_NAME type    -f config.plist -k CFBundleName
EOF
}

# Per-command help
command_help() {
    local cmd="$1"
    case "$cmd" in
        create)
            cat <<EOF
${C_BOLD}create${C_RESET} — Create a new empty plist file (xml1 format).

Usage: $SCRIPT_NAME create -f <path> [-s]

Options:
  -f, --file <path>     Path for the new plist file ${C_BOLD}(required)${C_RESET}
  -s, --silent          Suppress success messages

The file is always created in xml1 format (native macOS plist).
Use 'convert' afterwards if you need a different format.
EOF
            ;;
        read)
            cat <<EOF
${C_BOLD}read${C_RESET} — Print plist contents in human-readable form.

Usage: $SCRIPT_NAME read -f <path>

Options:
  -f, --file <path>     Plist file to read ${C_BOLD}(required)${C_RESET}
EOF
            ;;
        import)
            cat <<EOF
${C_BOLD}import${C_RESET} — Import a JSON file and convert it to a plist.

Usage: $SCRIPT_NAME import -f <json_file> [-o <output.plist>] [-s]

Options:
  -f, --file <path>     Source JSON file ${C_BOLD}(required)${C_RESET}
  -o, --output <path>   Output plist file (default: same name with .plist extension)
  -s, --silent          Suppress success messages

The JSON file must represent a valid property list structure.
The output is always in xml1 format.
EOF
            ;;
        convert)
            cat <<EOF
${C_BOLD}convert${C_RESET} — Convert a plist file to another format.

Usage: $SCRIPT_NAME convert -f <path> -F <format> [-o <output>] [-r] [-s]

Options:
  -f, --file <path>     Source plist file ${C_BOLD}(required)${C_RESET}
  -F, --format <fmt>    Target: xml1, binary1, json, swift, objc ${C_BOLD}(required)${C_RESET}
  -o, --output <path>   Output file (default: converts in-place)
  -r, --readable        Pretty-print JSON output
  -s, --silent          Suppress success messages
EOF
            ;;
        lint)
            cat <<EOF
${C_BOLD}lint${C_RESET} — Validate plist file syntax.

Usage: $SCRIPT_NAME lint -f <path> [-s]

Options:
  -f, --file <path>     Plist file to validate ${C_BOLD}(required)${C_RESET}
  -s, --silent          Suppress success messages
EOF
            ;;
        insert)
            cat <<EOF
${C_BOLD}insert${C_RESET} — Insert a key-value pair into a plist.

Usage: $SCRIPT_NAME insert -f <path> -k <keypath> -t <type> [-v <value>] [-a] [-s]

Options:
  -f, --file <path>     Plist file ${C_BOLD}(required)${C_RESET}
  -k, --key <keypath>   Key path to insert at ${C_BOLD}(required)${C_RESET}
  -t, --type <type>     Value type ${C_BOLD}(required)${C_RESET}
  -v, --value <val>     Value to set (not needed for array/dictionary)
  -a, --append          Append value to an existing array
  -s, --silent          Suppress success messages

Note: For types 'array' and 'dictionary', no value is needed — an empty
      container is created. Use -a/--append to add items to an array.
EOF
            ;;
        replace)
            cat <<EOF
${C_BOLD}replace${C_RESET} — Replace an existing value in a plist.

Usage: $SCRIPT_NAME replace -f <path> -k <keypath> -t <type> -v <value> [-s]

Options:
  -f, --file <path>     Plist file ${C_BOLD}(required)${C_RESET}
  -k, --key <keypath>   Key path to replace ${C_BOLD}(required)${C_RESET}
  -t, --type <type>     Value type ${C_BOLD}(required)${C_RESET}
  -v, --value <val>     New value ${C_BOLD}(required)${C_RESET}
  -s, --silent          Suppress success messages
EOF
            ;;
        remove)
            cat <<EOF
${C_BOLD}remove${C_RESET} — Remove a key from a plist.

Usage: $SCRIPT_NAME remove -f <path> -k <keypath> [-s]

Options:
  -f, --file <path>     Plist file ${C_BOLD}(required)${C_RESET}
  -k, --key <keypath>   Key path to remove ${C_BOLD}(required)${C_RESET}
  -s, --silent          Suppress success messages
EOF
            ;;
        extract)
            cat <<EOF
${C_BOLD}extract${C_RESET} — Extract and print a value at a keypath.

Usage: $SCRIPT_NAME extract -f <path> -k <keypath> [-F <format>] [-r]

Options:
  -f, --file <path>     Plist file ${C_BOLD}(required)${C_RESET}
  -k, --key <keypath>   Key path to extract ${C_BOLD}(required)${C_RESET}
  -F, --format <fmt>    Output format: xml1, json, raw (default: xml1)
  -r, --readable        Pretty-print JSON output
EOF
            ;;
        type)
            cat <<EOF
${C_BOLD}type${C_RESET} — Print the type of a value at a keypath.

Usage: $SCRIPT_NAME type -f <path> -k <keypath>

Options:
  -f, --file <path>     Plist file ${C_BOLD}(required)${C_RESET}
  -k, --key <keypath>   Key path to inspect ${C_BOLD}(required)${C_RESET}
EOF
            ;;
        *)
            usage
            ;;
    esac
}

# ==========================================================================
# Command Implementations
# ==========================================================================

# Function: validate_create_path
# Purpose: Ensure the target path for 'create' is a valid file destination.
#          Rejects existing directories, paths with no filename, and expects
#          a .plist extension.
# Arguments: $1 = resolved path
validate_create_path() {
    local file="$1"
    local base
    base="$(basename "$file")"

    # Reject if the path is an existing directory
    if [[ -d "$file" ]]; then
        die "'$file' is a directory, not a file. Please provide a full file path (e.g. /path/to/config.plist)."
        return $?
    fi

    # Reject if the basename has no extension
    if [[ "$base" != *.* ]]; then
        die "'$base' has no file extension. Please include the .plist extension (e.g. config.plist)."
        return $?
    fi

    # Warn if the extension is not .plist
    if [[ "$base" != *.plist ]]; then
        print_msg warn "File '$base' does not use the standard .plist extension."
    fi

    return 0
}

# Function: cmd_create
# Purpose: Create a new empty plist file in xml1 (native macOS) format.
cmd_create() {
    local file="" silent=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file)   file="$(resolve_path "$2")"; shift 2 ;;
            -s|--silent) silent=true; shift ;;
            -h|--help)   command_help create; return 0 ;;
            *) die "Unknown option for create: $1"; return $? ;;
        esac
    done

    if [[ -z "$file" ]]; then die "No file specified. Use -f <path>."; return $?; fi
    validate_create_path "$file" || return $?

    # Ensure the parent directory exists
    local parent_dir
    parent_dir="$(dirname "$file")"
    if [[ ! -d "$parent_dir" ]]; then
        die "Parent directory does not exist: $parent_dir"
        return $?
    fi

    if [[ -f "$file" ]]; then
        print_msg warn "File already exists: $file"
        read -rp "Overwrite? [y/N] " confirm
        [[ "$confirm" =~ ^[Yy]$ ]] || { print_msg info "Aborted."; return 1; }
    fi

    "$PLUTIL_CMD" -create xml1 "$file"
    $silent || print_msg success "Created plist: $file"
}

# Function: cmd_read
# Purpose: Print plist contents in human-readable form.
cmd_read() {
    local file=""

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file) file="$(resolve_path "$2")"; shift 2 ;;
            -h|--help) command_help read; return 0 ;;
            *) die "Unknown option for read: $1"; return $? ;;
        esac
    done

    require_file "$file" || return $?
    "$PLUTIL_CMD" -p "$file"
}

# Function: cmd_convert
# Purpose: Convert a plist file to another format.
cmd_convert() {
    local file="" format="" output="" silent=false readable=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file)     file="$(resolve_path "$2")"; shift 2 ;;
            -F|--format)   format="$2"; shift 2 ;;
            -o|--output)   output="$(resolve_path "$2")"; shift 2 ;;
            -r|--readable) readable=true; shift ;;
            -s|--silent)   silent=true; shift ;;
            -h|--help)     command_help convert; return 0 ;;
            *) die "Unknown option for convert: $1"; return $? ;;
        esac
    done

    require_file "$file" || return $?
    if [[ -z "$format" ]]; then die "No format specified. Use -F <format>."; return $?; fi
    validate_convert_format "$format" || return $?

    local args=(-convert "$format")
    $readable && args+=(-r)
    [[ -n "$output" ]] && args+=(-o "$output")
    args+=("$file")

    "$PLUTIL_CMD" "${args[@]}"

    if $silent; then
        return 0
    fi

    if [[ -n "$output" ]]; then
        print_msg success "Converted to $format: $file -> $output"
    else
        print_msg success "Converted $file to $format (in-place)."
    fi
}

# Function: cmd_import
# Purpose: Import a JSON file as a plist (xml1).
cmd_import() {
    local file="" output="" silent=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file)   file="$(resolve_path "$2")"; shift 2 ;;
            -o|--output) output="$(resolve_path "$2")"; shift 2 ;;
            -s|--silent) silent=true; shift ;;
            -h|--help)   command_help import; return 0 ;;
            *) die "Unknown option for import: $1"; return $? ;;
        esac
    done

    require_file "$file" || return $?

    # Default output: same basename with .plist extension
    if [[ -z "$output" ]]; then
        local base dir
        base="$(basename "$file")"
        dir="$(dirname "$file")"
        output="${dir}/${base%.*}.plist"
    fi

    if [[ -f "$output" ]]; then
        print_msg warn "File already exists: $output"
        read -rp "Overwrite? [y/N] " confirm
        [[ "$confirm" =~ ^[Yy]$ ]] || { print_msg info "Aborted."; return 1; }
    fi

    "$PLUTIL_CMD" -convert xml1 "$file" -o "$output"
    $silent || print_msg success "Imported $file -> $output"
}

# Function: cmd_lint
# Purpose: Validate plist file syntax.
cmd_lint() {
    local file="" silent=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file)   file="$(resolve_path "$2")"; shift 2 ;;
            -s|--silent) silent=true; shift ;;
            -h|--help)   command_help lint; return 0 ;;
            *) die "Unknown option for lint: $1"; return $? ;;
        esac
    done

    require_file "$file" || return $?

    if "$PLUTIL_CMD" -lint "$file"; then
        $silent || print_msg success "Plist syntax is valid: $file"
    else
        die "Plist syntax errors found in: $file"
    fi
}

# Function: cmd_insert
# Purpose: Insert a key-value pair into a plist.
cmd_insert() {
    local file="" keypath="" vtype="" value="" append=false silent=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file)   file="$(resolve_path "$2")"; shift 2 ;;
            -k|--key)    keypath="$2"; shift 2 ;;
            -t|--type)   vtype="$2"; shift 2 ;;
            -v|--value)  value="$2"; shift 2 ;;
            -a|--append) append=true; shift ;;
            -s|--silent) silent=true; shift ;;
            -h|--help)   command_help insert; return 0 ;;
            *) die "Unknown option for insert: $1"; return $? ;;
        esac
    done

    require_file "$file" || return $?
    require_keypath "$keypath" || return $?
    if [[ -z "$vtype" ]]; then die "No type specified. Use -t <type>."; return $?; fi
    validate_type "$vtype" || return $?

    local args=(-insert "$keypath" "-$vtype")

    # array and dictionary types do not take a value argument
    if [[ "$vtype" != "array" && "$vtype" != "dictionary" ]]; then
        if [[ -z "$value" ]]; then die "No value specified. Use -v <value> (required for type '$vtype')."; return $?; fi
        args+=("$value")
    fi

    $append && args+=(-append)
    args+=("$file")

    "$PLUTIL_CMD" "${args[@]}"
    $silent || print_msg success "Inserted '$keypath' ($vtype) into $file"
}

# Function: cmd_replace
# Purpose: Replace an existing value in a plist.
cmd_replace() {
    local file="" keypath="" vtype="" value="" silent=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file)   file="$(resolve_path "$2")"; shift 2 ;;
            -k|--key)    keypath="$2"; shift 2 ;;
            -t|--type)   vtype="$2"; shift 2 ;;
            -v|--value)  value="$2"; shift 2 ;;
            -s|--silent) silent=true; shift ;;
            -h|--help)   command_help replace; return 0 ;;
            *) die "Unknown option for replace: $1"; return $? ;;
        esac
    done

    require_file "$file" || return $?
    require_keypath "$keypath" || return $?
    if [[ -z "$vtype" ]]; then die "No type specified. Use -t <type>."; return $?; fi
    validate_type "$vtype" || return $?
    if [[ -z "$value" ]]; then die "No value specified. Use -v <value>."; return $?; fi

    "$PLUTIL_CMD" -replace "$keypath" "-$vtype" "$value" "$file"
    $silent || print_msg success "Replaced '$keypath' ($vtype) in $file"
}

# Function: cmd_remove
# Purpose: Remove a key from a plist.
cmd_remove() {
    local file="" keypath="" silent=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file)   file="$(resolve_path "$2")"; shift 2 ;;
            -k|--key)    keypath="$2"; shift 2 ;;
            -s|--silent) silent=true; shift ;;
            -h|--help)   command_help remove; return 0 ;;
            *) die "Unknown option for remove: $1"; return $? ;;
        esac
    done

    require_file "$file" || return $?
    require_keypath "$keypath" || return $?

    "$PLUTIL_CMD" -remove "$keypath" "$file"
    $silent || print_msg success "Removed '$keypath' from $file"
}

# Function: cmd_extract
# Purpose: Extract and print a value at a keypath.
cmd_extract() {
    local file="" keypath="" format="xml1" readable=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file)     file="$(resolve_path "$2")"; shift 2 ;;
            -k|--key)      keypath="$2"; shift 2 ;;
            -F|--format)   format="$2"; shift 2 ;;
            -r|--readable) readable=true; shift ;;
            -h|--help)     command_help extract; return 0 ;;
            *) die "Unknown option for extract: $1"; return $? ;;
        esac
    done

    require_file "$file" || return $?
    require_keypath "$keypath" || return $?

    local args=(-extract "$keypath" "$format")
    $readable && args+=(-r)
    args+=(-o -)  # output to stdout
    args+=("$file")

    "$PLUTIL_CMD" "${args[@]}"
}

# Function: cmd_type
# Purpose: Print the type of a value at a keypath.
cmd_type() {
    local file="" keypath=""

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file) file="$(resolve_path "$2")"; shift 2 ;;
            -k|--key)  keypath="$2"; shift 2 ;;
            -h|--help) command_help type; return 0 ;;
            *) die "Unknown option for type: $1"; return $? ;;
        esac
    done

    require_file "$file" || return $?
    require_keypath "$keypath" || return $?

    "$PLUTIL_CMD" -type "$keypath" "$file"
}

# ==========================================================================
# Interactive Menu
# ==========================================================================

# Function: prompt_input
# Purpose: Prompt the user for input with a default value.
# Arguments: $1 = prompt text, $2 = default value (optional)
# Returns: The user's input via stdout.
prompt_input() {
    local prompt="$1" default="${2:-}" input
    if [[ -n "$default" ]]; then
        read -rp "$(echo -e "${C_CYAN}$prompt${C_RESET} [${default}]: ")" input
        echo "${input:-$default}"
    else
        read -rp "$(echo -e "${C_CYAN}$prompt${C_RESET}: ")" input
        echo "$input"
    fi
}

# Function: prompt_choice
# Purpose: Prompt the user to choose from a numbered list.
#          Uses single-key input (no Enter) when there are 9 or fewer options.
# Arguments: $1 = prompt, $2.. = options
# Returns: The chosen option via stdout.
prompt_choice() {
    local prompt="$1"; shift
    local options=("$@")
    local count=${#options[@]}
    local i

    echo -e "${C_CYAN}$prompt${C_RESET}" >&2
    for i in "${!options[@]}"; do
        echo -e "  ${C_BOLD}$((i+1)))${C_RESET} ${options[$i]}" >&2
    done

    local choice
    if (( count <= 9 )); then
        # Single-key input
        while true; do
            echo -ne "  ${C_DIM}[1-${count}]:${C_RESET} " >&2
            read -rsn1 choice
            echo "$choice" >&2
            if [[ "$choice" =~ ^[1-9]$ ]] && (( choice >= 1 && choice <= count )); then
                echo "${options[$((choice-1))]}"
                return 0
            fi
            echo "  Invalid choice." >&2
        done
    else
        # Fallback: Enter required for 10+ options
        while true; do
            read -rp "  [1-${count}]: " choice
            if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= count )); then
                echo "${options[$((choice-1))]}"
                return 0
            fi
            echo "  Invalid choice." >&2
        done
    fi
}

# Function: menu_select
# Purpose: Display a numbered menu with single-key input and optional shortcut
#          keys for navigation (b=back, q=quit, h=help). No Enter required.
# Arguments: $1 = prompt, $2 = shortcuts (e.g. "bq", "qh"), $3.. = options
# Returns via stdout: chosen option text, __quit__, __back__, or __help__
menu_select() {
    local prompt="$1"; shift
    local shortcuts="$1"; shift
    local options=("$@")
    local count=${#options[@]}
    local i

    echo -e "${C_CYAN}$prompt${C_RESET}" >&2
    for i in "${!options[@]}"; do
        echo -e "  ${C_BOLD}$((i+1)))${C_RESET} ${options[$i]}" >&2
    done

    # Build shortcut hint line
    local hints=()
    [[ "$shortcuts" == *b* ]] && hints+=("b=back")
    [[ "$shortcuts" == *h* ]] && hints+=("h=help")
    [[ "$shortcuts" == *q* ]] && hints+=("q=quit")
    if [[ ${#hints[@]} -gt 0 ]]; then
        echo -ne "  ${C_DIM}" >&2
        local h
        for h in "${hints[@]}"; do echo -ne "$h  " >&2; done
        echo -e "${C_RESET}" >&2
    fi

    local choice
    while true; do
        echo -ne "  ${C_DIM}▸${C_RESET} " >&2
        read -rsn1 choice
        echo "$choice" >&2

        # Check shortcuts
        if [[ "$shortcuts" == *q* ]] && [[ "$choice" =~ ^[Qq]$ ]]; then
            echo "__quit__"; return 0
        fi
        if [[ "$shortcuts" == *b* ]] && [[ "$choice" =~ ^[Bb]$ ]]; then
            echo "__back__"; return 0
        fi
        if [[ "$shortcuts" == *h* ]] && [[ "$choice" =~ ^[Hh]$ ]]; then
            echo "__help__"; return 0
        fi

        if [[ "$choice" =~ ^[1-9]$ ]] && (( choice >= 1 && choice <= count )); then
            echo "${options[$((choice-1))]}"
            return 0
        fi
        echo "  Invalid choice." >&2
    done
}

# Function: prompt_yesno
# Purpose: Prompt user for a yes/no question. Single-key, no Enter required.
# Arguments: $1 = prompt, $2 = default (y/n)
# Returns: 0 for yes, 1 for no
prompt_yesno() {
    local prompt="$1" default="${2:-n}" input
    if [[ "$default" == "y" ]]; then
        echo -ne "${C_CYAN}$prompt${C_RESET} [Y/n]: " >&2
        read -rsn1 input
        input="${input:-y}"
        echo "$input" >&2
        [[ "$input" =~ ^[Nn]$ ]] && return 1 || return 0
    else
        echo -ne "${C_CYAN}$prompt${C_RESET} [y/N]: " >&2
        read -rsn1 input
        input="${input:-n}"
        echo "$input" >&2
        [[ "$input" =~ ^[Yy]$ ]] && return 0 || return 1
    fi
}

# --- Menu State ---
# Tracks the currently active plist file across menu iterations so the user
# does not have to re-enter it for every operation.
MENU_WORKING_FILE=""

# Function: menu_separator
# Purpose: Print a visual separator line between menu iterations.
menu_separator() {
    echo ""
    echo -e "${C_DIM}────────────────────────────────────────${C_RESET}"
    echo ""
}

# Function: menu_banner
# Purpose: Print the one-time startup banner.
menu_banner() {
    echo ""
    echo -e "${C_BOLD}╔══════════════════════════════════════════╗${C_RESET}"
    echo -e "${C_BOLD}║    Plist Generator — Interactive Mode    ║${C_RESET}"
    echo -e "${C_BOLD}╠══════════════════════════════════════════╣${C_RESET}"
    echo -e "${C_BOLD}║${C_RESET}  A friendly wrapper around macOS plutil  ${C_BOLD}║${C_RESET}"
    echo -e "${C_BOLD}║${C_RESET}  Press ${C_CYAN}q${C_RESET} to quit, ${C_CYAN}b${C_RESET} to go back         ${C_BOLD}║${C_RESET}"
    echo -e "${C_BOLD}╚══════════════════════════════════════════╝${C_RESET}"
}

# Function: menu_status_bar
# Purpose: Show the current working file (if any) before each menu prompt.
menu_status_bar() {
    if [[ -n "$MENU_WORKING_FILE" ]]; then
        echo -e "${C_DIM}Working file:${C_RESET} ${C_BOLD}$MENU_WORKING_FILE${C_RESET}"
    else
        echo -e "${C_DIM}No working file set. Use 'Set file' or 'Create' to set one.${C_RESET}"
    fi
    echo ""
}

# Function: menu_prompt_file
# Purpose: Ask for a file path, defaulting to the current working file.
# Arguments: $1 = prompt text (e.g. "Plist file")
# Outputs:   The resolved file path via stdout. Also updates MENU_WORKING_FILE.
menu_prompt_file() {
    local prompt="${1:-Plist file}" file
    if [[ -n "$MENU_WORKING_FILE" ]]; then
        file="$(prompt_input "$prompt" "$MENU_WORKING_FILE")"
    else
        file="$(prompt_input "$prompt")"
    fi
    file="$(resolve_path "$file")"
    MENU_WORKING_FILE="$file"
    echo "$file"
}

# Function: menu_file_ops
# Purpose: File operations submenu. Has its own loop.
#          Returns 0 on back, 1 on quit.
menu_file_ops() {
    while true; do
        menu_separator
        menu_status_bar

        local cmd
        cmd="$(menu_select "File Operations:" "bq" \
            "create    — Create a new empty plist" \
            "import    — Import JSON as plist" \
            "read      — Print plist contents" \
            "convert   — Convert to another format" \
            "lint      — Validate plist syntax" \
            "extract   — Extract a value at keypath" \
            "set-file  — Change the working file")"

        local keyword="${cmd%% *}"
        keyword="$(echo "$keyword" | tr -d '[:space:]')"

        echo ""

        case "$keyword" in
            __back__) return 0 ;;
            __quit__)
                print_msg info "Goodbye!"
                return 1
                ;;
            create)
                local file _path_ok
                file="$(prompt_input "File path for new plist (e.g. ~/config.plist)" "${MENU_WORKING_FILE:-}")"
                file="$(resolve_path "$file")"

                _path_ok=true
                if [[ -d "$file" ]]; then
                    print_msg error "'$file' is a directory. Provide a full file path (e.g. /path/to/config.plist)."
                    _path_ok=false
                fi
                if $_path_ok; then
                    local base
                    base="$(basename "$file")"
                    if [[ "$base" != *.* ]]; then
                        print_msg error "'$base' has no file extension. Use .plist (e.g. config.plist)."
                        _path_ok=false
                    fi
                fi

                if $_path_ok; then
                    if cmd_create -f "$file"; then
                        MENU_WORKING_FILE="$file"
                        print_msg info "Working file set to: $file"
                    fi
                fi
                ;;
            import)
                local json_file output
                json_file="$(prompt_input "JSON file to import")"
                json_file="$(resolve_path "$json_file")"
                output="$(prompt_input "Output plist file" "${json_file%.*}.plist")"
                output="$(resolve_path "$output")"
                if cmd_import -f "$json_file" -o "$output"; then
                    MENU_WORKING_FILE="$output"
                    print_msg info "Working file set to: $output"
                fi
                ;;
            read)
                local file
                file="$(menu_prompt_file "Plist file to read")"
                cmd_read -f "$file"
                ;;
            convert)
                local file format output readable_flag
                file="$(menu_prompt_file "Source plist file")"
                format="$(prompt_choice "Target format:" "xml1" "binary1" "json" "swift" "objc")"
                output="$(prompt_input "Output file (leave empty for in-place)" "")"
                readable_flag=""
                if [[ "$format" == "json" ]]; then
                    prompt_yesno "Pretty-print JSON?" "y" && readable_flag="-r"
                fi
                local args=(-f "$file" -F "$format")
                [[ -n "$output" ]] && args+=(-o "$output")
                [[ -n "$readable_flag" ]] && args+=("$readable_flag")
                cmd_convert "${args[@]}"
                ;;
            lint)
                local file
                file="$(menu_prompt_file "Plist file to validate")"
                cmd_lint -f "$file"
                ;;
            extract)
                local file keypath format readable_flag
                file="$(menu_prompt_file "Plist file")"
                keypath="$(prompt_input "Key path to extract")"
                format="$(prompt_choice "Output format:" "xml1" "json" "raw")"
                readable_flag=""
                if [[ "$format" == "json" ]]; then
                    prompt_yesno "Pretty-print JSON?" "y" && readable_flag="-r"
                fi
                local args=(-f "$file" -k "$keypath" -F "$format")
                [[ -n "$readable_flag" ]] && args+=("$readable_flag")
                cmd_extract "${args[@]}"
                ;;
            set-file)
                local file
                file="$(prompt_input "Path to plist file" "${MENU_WORKING_FILE:-}")"
                file="$(resolve_path "$file")"
                if [[ -f "$file" ]]; then
                    MENU_WORKING_FILE="$file"
                    print_msg success "Working file set to: $file"
                else
                    print_msg warn "File does not exist yet: $file"
                    if prompt_yesno "Set it as working file anyway?" "y"; then
                        MENU_WORKING_FILE="$file"
                        print_msg info "Working file set to: $file"
                    fi
                fi
                ;;
            *) print_msg warn "Unknown selection." ;;
        esac
    done
}

# Function: menu_key_ops
# Purpose: Key operations submenu. Has its own loop.
#          Returns 0 on back, 1 on quit.
menu_key_ops() {
    while true; do
        menu_separator
        menu_status_bar

        local cmd
        cmd="$(menu_select "Key Operations:" "bq" \
            "insert    — Insert a key-value pair" \
            "replace   — Replace a key-value pair" \
            "remove    — Remove a key" \
            "type      — Show type of a value")"

        local keyword="${cmd%% *}"
        keyword="$(echo "$keyword" | tr -d '[:space:]')"

        echo ""

        case "$keyword" in
            __back__) return 0 ;;
            __quit__)
                print_msg info "Goodbye!"
                return 1
                ;;
            insert)
                local file keypath vtype value
                file="$(menu_prompt_file "Plist file")"
                keypath="$(prompt_input "Key path (e.g. CFBundleName)")"
                vtype="$(prompt_choice "Value type:" "string" "integer" "float" "bool" "array" "dictionary" "data" "date" "xml" "json")"
                local args=(-f "$file" -k "$keypath" -t "$vtype")
                if [[ "$vtype" != "array" && "$vtype" != "dictionary" ]]; then
                    value="$(prompt_input "Value")"
                    args+=(-v "$value")
                fi
                if prompt_yesno "Append to array?" "n"; then
                    args+=(-a)
                fi
                cmd_insert "${args[@]}"

                # Offer to keep inserting more keys into the same file
                while prompt_yesno "Insert another key into this file?" "n"; do
                    keypath="$(prompt_input "Key path")"
                    vtype="$(prompt_choice "Value type:" "string" "integer" "float" "bool" "array" "dictionary" "data" "date" "xml" "json")"
                    args=(-f "$file" -k "$keypath" -t "$vtype")
                    if [[ "$vtype" != "array" && "$vtype" != "dictionary" ]]; then
                        value="$(prompt_input "Value")"
                        args+=(-v "$value")
                    fi
                    if prompt_yesno "Append to array?" "n"; then
                        args+=(-a)
                    fi
                    cmd_insert "${args[@]}"
                done
                ;;
            replace)
                local file keypath vtype value
                file="$(menu_prompt_file "Plist file")"
                keypath="$(prompt_input "Key path to replace")"
                vtype="$(prompt_choice "Value type:" "string" "integer" "float" "bool" "data" "date" "xml" "json")"
                value="$(prompt_input "New value")"
                cmd_replace -f "$file" -k "$keypath" -t "$vtype" -v "$value"
                ;;
            remove)
                local file keypath
                file="$(menu_prompt_file "Plist file")"
                keypath="$(prompt_input "Key path to remove")"
                cmd_remove -f "$file" -k "$keypath"
                ;;
            type)
                local file keypath
                file="$(menu_prompt_file "Plist file")"
                keypath="$(prompt_input "Key path to inspect")"
                cmd_type -f "$file" -k "$keypath"
                ;;
            *) print_msg warn "Unknown selection." ;;
        esac
    done
}

# Function: menu_main
# Purpose: Show the top-level menu and dispatch to submenus.
#          Returns 0 to continue, 1 to quit.
menu_main() {
    local cmd
    cmd="$(menu_select "Main Menu:" "qh" \
        "File operations  — create, read, convert, lint, extract" \
        "Key operations   — insert, replace, remove, type")"

    echo ""

    case "$cmd" in
        __quit__)
            print_msg info "Goodbye!"
            return 1
            ;;
        __help__)
            usage
            return 0
            ;;
        *)
            local keyword="${cmd%% *}"
            keyword="$(echo "$keyword" | tr -d '[:space:]')"
            case "$keyword" in
                File)
                    menu_file_ops
                    return $?
                    ;;
                Key)
                    menu_key_ops
                    return $?
                    ;;
                *) print_msg warn "Unknown selection." ;;
            esac
            ;;
    esac
    return 0
}

# Function: interactive_menu
# Purpose: Run the persistent menu loop. Shows a banner once, then repeatedly
#          prompts the user via hierarchical menus until they choose to quit.
interactive_menu() {
    menu_banner
    IN_MENU_LOOP=true

    local keep_running=true
    while $keep_running; do
        menu_separator
        menu_status_bar

        local rc=0
        menu_main || rc=$?
        if [[ $rc -eq 1 ]]; then
            keep_running=false
        fi
    done

    IN_MENU_LOOP=false
}

# ==========================================================================
# Main Entry Point
# ==========================================================================

main() {
    require_plutil

    # No arguments → interactive mode
    if [[ $# -eq 0 ]]; then
        interactive_menu
        return $?
    fi

    local command="$1"; shift

    case "$command" in
        create)  cmd_create "$@" ;;
        import)  cmd_import "$@" ;;
        read)    cmd_read "$@" ;;
        convert) cmd_convert "$@" ;;
        lint)    cmd_lint "$@" ;;
        insert)  cmd_insert "$@" ;;
        replace) cmd_replace "$@" ;;
        remove)  cmd_remove "$@" ;;
        extract) cmd_extract "$@" ;;
        type)    cmd_type "$@" ;;
        -h|--help|help)
            usage ;;
        *)
            die "Unknown command: '$command'. Run '$SCRIPT_NAME --help' for usage." ;;
    esac
}

main "$@"
