#!/usr/bin/env bash
set -euo pipefail

if [[ "${SUDOKU_OFFICIAL_BIN:-}" == "" ]]; then
  if command -v sudoku >/dev/null 2>&1; then
    export SUDOKU_OFFICIAL_BIN
    SUDOKU_OFFICIAL_BIN="$(command -v sudoku)"
  else
    echo "Missing SUDOKU_OFFICIAL_BIN and `sudoku` not found in PATH." >&2
    echo "Build it from https://github.com/SUDOKU-ASCII/sudoku and set:" >&2
    echo "  export SUDOKU_OFFICIAL_BIN=/path/to/sudoku" >&2
    exit 1
  fi
fi

export SUDOKU_OFFICIAL_INTEROP=1
go test ./protocol/sudoku -run TestSudoku_OfficialInterop -count=1

