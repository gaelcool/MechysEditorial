#!/usr/bin/env bash
set -euo pipefail

# ATOM: tiny local admin helper for Mechy Classics.
# It writes books/authors with transactions so partial rows do not survive.

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/.." && pwd)"
db_path="${DB_PATH:-${repo_root}/data/MC.db}"

require_sqlite() {
    if ! command -v sqlite3 >/dev/null 2>&1; then
        echo "sqlite3 is required." >&2
        exit 1
    fi

    if [[ ! -f "${db_path}" ]]; then
        echo "Missing database: ${db_path}" >&2
        echo "Run scripts/install.sh first, or set DB_PATH=/path/to/MC.db." >&2
        exit 1
    fi
}

sql_literal() {
    local value="${1:-}"

    if [[ -z "${value}" ]]; then
        printf 'NULL'
        return
    fi

    value="${value//\'/\'\'}"
    printf "'%s'" "${value}"
}

prompt_required() {
    local label="$1"
    local value=""

    while [[ -z "${value}" ]]; do
        read -r -p "${label}: " value
        if [[ -z "${value}" ]]; then
            echo "Required."
        fi
    done

    printf '%s' "${value}"
}

prompt_optional() {
    local label="$1"
    local value=""

    read -r -p "${label}: " value
    printf '%s' "${value}"
}

confirm() {
    local prompt="$1"
    local answer=""

    read -r -p "${prompt} [y/N]: " answer
    [[ "${answer}" =~ ^[Yy]([Ee][Ss])?$ ]]
}

slug_to_title() {
    printf '%s' "$1" |
        sed -E 's/[-_]+/ /g; s/(^| )([a-z])/\1\u\2/g'
}

extension_from_path() {
    local path="$1"

    if [[ "${path}" == *.* ]]; then
        printf '%s' "${path##*.}"
    fi
}

run_sql() {
    local sql="$1"

    printf '%s\n' "${sql}" | sqlite3 -batch "${db_path}"
}

print_pairs() {
    local -n pairs="$1"
    local key

    echo
    echo "Review:"
    for key in "${!pairs[@]}"; do
        printf '  %-12s %s\n' "${key}:" "${pairs[$key]}"
    done
    echo
}

list_authors() {
    sqlite3 -header -column "${db_path}" \
        "SELECT id, name FROM authors ORDER BY id;"
}

normalize_author_ids() {
    local raw="$1"
    local cleaned="${raw// /}"
    local id
    declare -A seen=()
    local ids=()

    if [[ ! "${cleaned}" =~ ^[0-9]+(,[0-9]+)*$ ]]; then
        return 1
    fi

    IFS=',' read -r -a ids <<< "${cleaned}"
    for id in "${ids[@]}"; do
        seen["${id}"]=1
    done

    printf '%s\n' "${!seen[@]}" | sort -n | paste -sd, -
}

validate_author_ids() {
    local author_csv="$1"
    local expected actual

    expected="$(tr ',' '\n' <<< "${author_csv}" | wc -l | tr -d ' ')"
    actual="$(sqlite3 "${db_path}" \
        "SELECT COUNT(*) FROM authors WHERE id IN (${author_csv});")"

    [[ "${actual}" == "${expected}" ]]
}

csv_to_sql_values() {
    local csv="$1"
    local id
    local -a ids=()
    local values=()

    IFS=',' read -r -a ids <<< "${csv}"
    for id in "${ids[@]}"; do
        values+=("(${id})")
    done

    local joined="${values[*]}"
    printf '%s' "${joined// /,}"
}

add_author() {
    declare -A opcs=()

    opcs[name]="$(prompt_required "Author name")"
    opcs[avatar]="$(prompt_optional "Avatar path, optional")"
    opcs[file_path]="$(prompt_optional "Author page/file path, optional")"
    opcs[bio]="$(prompt_optional "Bio, optional")"
    opcs[apa]="$(prompt_optional "APA/reference note, optional")"
    opcs[contact_url]="$(prompt_optional "Contact URL, optional")"

    print_pairs opcs
    confirm "Insert this author?" || {
        echo "Cancelled."
        return
    }

    local sql
    sql="$(cat <<SQL
.bail on
PRAGMA foreign_keys = ON;
BEGIN IMMEDIATE;
INSERT INTO authors (name, avatar, file_path, bio, apa, contact_url)
VALUES (
    $(sql_literal "${opcs[name]}"),
    $(sql_literal "${opcs[avatar]}"),
    $(sql_literal "${opcs[file_path]}"),
    $(sql_literal "${opcs[bio]}"),
    $(sql_literal "${opcs[apa]}"),
    $(sql_literal "${opcs[contact_url]}")
);
COMMIT;
PRAGMA foreign_key_check;
SQL
)"

    run_sql "${sql}"
    echo "Author inserted."
}

add_book() {
    declare -A opcs=()

    opcs[slug]="$(prompt_required "Book slug")"

    local guessed_title
    guessed_title="$(slug_to_title "${opcs[slug]}")"
    if confirm "Use '${guessed_title}' as title?"; then
        opcs[title]="${guessed_title}"
    else
        opcs[title]="$(prompt_required "Book title")"
    fi

    opcs[description]="$(prompt_required "Description")"
    opcs[print_date]="$(prompt_optional "Print date YYYY-MM-DD, optional")"
    opcs[cover]="$(prompt_optional "Cover path, optional")"
    opcs[file_path]="$(prompt_optional "Book file path, optional")"

    local guessed_type
    guessed_type="$(extension_from_path "${opcs[file_path]}")"
    if [[ -n "${guessed_type}" ]] && confirm "Use '${guessed_type}' as media type?"; then
        opcs[media_type]="${guessed_type}"
    else
        opcs[media_type]="$(prompt_optional "Media type, optional")"
    fi

    echo
    echo "Existing authors:"
    list_authors
    echo

    local raw_author_ids author_csv
    read -r -p "Author IDs for this book, comma-separated: " raw_author_ids
    if ! author_csv="$(normalize_author_ids "${raw_author_ids}")"; then
        echo "Invalid author list. Use numbers like: 1,2,5" >&2
        return 1
    fi

    if ! validate_author_ids "${author_csv}"; then
        echo "One or more author IDs do not exist. No rows were written." >&2
        return 1
    fi

    opcs[author_ids]="${author_csv}"

    print_pairs opcs
    confirm "Insert this book and its book_authors links atomically?" || {
        echo "Cancelled."
        return
    }

    local author_values sql
    author_values="$(csv_to_sql_values "${opcs[author_ids]}")"

    sql="$(cat <<SQL
.bail on
PRAGMA foreign_keys = ON;
BEGIN IMMEDIATE;
CREATE TEMP TABLE __atom_new_book (id INTEGER NOT NULL);
CREATE TEMP TABLE __atom_requested_author (id INTEGER PRIMARY KEY);
CREATE TEMP TABLE __atom_author_check (
    expected INTEGER NOT NULL,
    actual INTEGER NOT NULL,
    CHECK (expected = actual)
);
INSERT INTO __atom_requested_author (id)
VALUES ${author_values};
INSERT INTO __atom_author_check (expected, actual)
SELECT
    (SELECT COUNT(*) FROM __atom_requested_author),
    (SELECT COUNT(*)
     FROM authors a
     JOIN __atom_requested_author r ON r.id = a.id);
INSERT INTO books (slug, title, description, print_date, cover, media_type, file_path)
VALUES (
    $(sql_literal "${opcs[slug]}"),
    $(sql_literal "${opcs[title]}"),
    $(sql_literal "${opcs[description]}"),
    $(sql_literal "${opcs[print_date]}"),
    $(sql_literal "${opcs[cover]}"),
    $(sql_literal "${opcs[media_type]}"),
    $(sql_literal "${opcs[file_path]}")
);
INSERT INTO __atom_new_book (id)
VALUES (last_insert_rowid());
INSERT INTO book_authors (book_id, author_id)
SELECT nb.id, a.id
FROM __atom_new_book nb
JOIN __atom_requested_author a;
DROP TABLE __atom_author_check;
DROP TABLE __atom_requested_author;
DROP TABLE __atom_new_book;
COMMIT;
PRAGMA foreign_key_check;
SQL
)"

    run_sql "${sql}"
    echo "Book and author links inserted."
}

menu() {
    while true; do
        echo
        echo "What operation will it be today?"
        read -r -p "A) Add author  B) Add book  L) List authors  Q) Quit: " choice

        case "${choice:0:1}" in
            A|a) add_author ;;
            B|b) add_book ;;
            L|l) list_authors ;;
            Q|q) echo "Cya."; exit 0 ;;
            *) echo "Invalid option: '${choice}'." ;;
        esac
    done
}

require_sqlite
menu
