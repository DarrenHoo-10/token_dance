#!/usr/bin/env python3
"""Fail if a Windows portable is not an x64 GUI-subsystem PE."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

IMAGE_FILE_MACHINE_AMD64 = 0x8664
IMAGE_NT_OPTIONAL_HDR32_MAGIC = 0x10B
IMAGE_NT_OPTIONAL_HDR64_MAGIC = 0x20B
IMAGE_SUBSYSTEM_WINDOWS_GUI = 2
MAX_PE_OFFSET = 150 * 1024 * 1024


def check_windows_gui_exe(path: Path) -> None:
    data = path.read_bytes()
    if len(data) < 64 or data[:2] != b"MZ":
        raise ValueError("Expected a Windows executable")
    offset = int.from_bytes(data[60:64], "little")
    optional = offset + 24
    if offset < 64 or optional + 70 > len(data) or offset > MAX_PE_OFFSET:
        raise ValueError("Invalid PE header")
    if data[offset : offset + 4] != b"PE\0\0":
        raise ValueError("Invalid PE header")
    machine = int.from_bytes(data[offset + 4 : offset + 6], "little")
    if machine != IMAGE_FILE_MACHINE_AMD64:
        raise ValueError("Expected a Windows x64 executable")
    magic = int.from_bytes(data[optional : optional + 2], "little")
    if magic not in (IMAGE_NT_OPTIONAL_HDR32_MAGIC, IMAGE_NT_OPTIONAL_HDR64_MAGIC):
        raise ValueError("Invalid PE optional header")
    subsystem = int.from_bytes(data[optional + 68 : optional + 70], "little")
    if subsystem != IMAGE_SUBSYSTEM_WINDOWS_GUI:
        raise ValueError(
            "BLOCKED: TokenDance.exe must use the Windows GUI subsystem"
        )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("path", type=Path, help="Path to TokenDance.exe")
    args = parser.parse_args()
    try:
        check_windows_gui_exe(args.path)
    except ValueError as error:
        print(error, file=sys.stderr)
        raise SystemExit(1)
    print(f"OK: {args.path} is a Windows x64 GUI executable")


if __name__ == "__main__":
    main()
