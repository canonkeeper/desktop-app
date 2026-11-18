// SPDX-License-Identifier: AGPL-3.0-only
// The DLLs the programs and libraries of the Windows installer import (specs/2026-11-18-desk-windows-chat.md WC2.2):
//
//   node scripts/desktop-app-imports.mjs <file or directory>…
//
// reads the import table and the table of delayed imports of every PE file under the paths — the programs and DLLs of
// the shell, the backend, the bridge, Codex, PostgreSQL and of the installer itself — and refuses an import that is
// neither a file of the package nor a DLL of every clean Windows 10 and 11 (WINDOWS_DLLS; the UCRT api-ms-win-crt-*
// among them, Windows has it since 10). VCRUNTIME*, MSVCP*, CONCRT* and VCOMP* come with Visual C++ Redistributable, not
// with Windows: such an import is a refusal while the package does not carry the DLL — a clean Windows ends the program
// with 0xc0000135 before its first line. The standard library of Node only; it reads the files on any system.
import { lstatSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

/**
 * The DLLs of a clean Windows 10 and 11 the files of the installer import, by their names in lower case: each is a part
 * of Windows itself, in System32 of every edition — the closed list of what the installer of 0.1.3 imported but the C
 * runtime of Visual C++. An import of the shell, Codex, PostgreSQL or NSIS that is not here fails the check until it is
 * known to be a part of Windows, or the package carries it.
 */
export const WINDOWS_DLLS = new Set([
  'advapi32.dll', 'bcrypt.dll', 'bcryptprimitives.dll', 'combase.dll', 'comctl32.dll', 'comdlg32.dll', 'crypt32.dll',
  'dwmapi.dll', 'gdi32.dll', 'imm32.dll', 'iphlpapi.dll', 'kernel32.dll', 'netapi32.dll', 'ntdll.dll', 'ole32.dll',
  'oleaut32.dll', 'secur32.dll', 'shell32.dll', 'shlwapi.dll', 'tdh.dll', 'user32.dll', 'userenv.dll', 'version.dll',
  'winhttp.dll', 'ws2_32.dll',
  // API sets of Windows the shell and Codex import (the Rust standard library, WinRT, the sandbox of Codex).
  'api-ms-win-core-apiquery-l2-1-0.dll', 'api-ms-win-core-synch-l1-2-0.dll', 'api-ms-win-core-winrt-error-l1-1-0.dll',
  'api-ms-win-core-winrt-l1-1-0.dll', 'api-ms-win-security-base-l1-2-2.dll',
]);

/** The API sets of the Universal C Runtime: a part of Windows since 10 (ucrtbase.dll), one name for each of its parts. */
export const UCRT_DLLS = new Set([
  'conio', 'convert', 'environment', 'filesystem', 'heap', 'locale', 'math', 'multibyte', 'private', 'process', 'runtime',
  'stdio', 'string', 'time', 'utility',
].map((part) => `api-ms-win-crt-${part}-l1-1-0.dll`));

/** The DLLs of Visual C++ Redistributable, which a clean Windows has not. */
export const REDISTRIBUTABLE = /^(?:vcruntime|msvcp|concrt|vcomp)\d/iu;

/** Whether a clean Windows 10 and 11 carries the DLL. */
export function windowsDll(name) {
  const lower = name.toLowerCase();
  return WINDOWS_DLLS.has(lower) || UCRT_DLLS.has(lower);
}

class Reader {
  constructor(bytes) {
    this.bytes = bytes;
  }

  u16(offset) {
    this.within(offset, 2);
    return this.bytes.readUInt16LE(offset);
  }

  u32(offset) {
    this.within(offset, 4);
    return this.bytes.readUInt32LE(offset);
  }

  u64(offset) {
    this.within(offset, 8);
    return this.bytes.readBigUInt64LE(offset);
  }

  within(offset, length) {
    if (!Number.isInteger(offset) || offset < 0 || offset + length > this.bytes.length) {
      throw new Error(`a PE file is cut short at ${offset}`);
    }
  }

  // name reads the zero-ended ASCII name of a DLL.
  name(offset) {
    const end = this.bytes.indexOf(0, offset);
    if (offset < 0 || end < 0 || end - offset > 255 || end === offset) throw new Error(`a PE file names a DLL badly at ${offset}`);
    const name = this.bytes.toString('latin1', offset, end);
    if (!/^[\x21-\x7e]+$/u.test(name)) throw new Error(`a PE file names a DLL badly at ${offset}`);
    return name;
  }
}

/**
 * The DLLs a PE file imports — its import table (`imports`) and its table of delayed imports (`delayed`) — or null for a
 * file that is no PE file. A PE file whose tables lie outside it is an error.
 */
export function peImports(bytes) {
  if (bytes.length < 64 || bytes[0] !== 0x4d || bytes[1] !== 0x5a) return null;
  const read = new Reader(bytes);
  const pe = read.u32(0x3c);
  if (pe + 24 > bytes.length || bytes.toString('latin1', pe, pe + 4) !== 'PE\0\0') return null;
  const sections = read.u16(pe + 6);
  const optionalSize = read.u16(pe + 20);
  const optional = pe + 24;
  const magic = read.u16(optional);
  if (magic !== 0x10b && magic !== 0x20b) throw new Error(`a PE file of an unknown kind ${magic.toString(16)}`);
  const plus = magic === 0x20b;
  const imageBase = plus ? read.u64(optional + 24) : BigInt(read.u32(optional + 28));
  const directories = read.u32(optional + (plus ? 108 : 92));
  const directory = (index) => (index < directories ? read.u32(optional + (plus ? 112 : 96) + index * 8) : 0);

  const table = optional + optionalSize;
  const headers = [];
  for (let index = 0; index < sections; index += 1) {
    const at = table + index * 40;
    headers.push({
      address: read.u32(at + 12), size: Math.max(read.u32(at + 8), read.u32(at + 16)),
      raw: read.u32(at + 20), rawSize: read.u32(at + 16),
    });
  }
  const offsetOf = (rva) => {
    const section = headers.find((entry) => rva >= entry.address && rva < entry.address + entry.size);
    if (!section || rva - section.address >= section.rawSize) throw new Error(`a PE file points outside itself at ${rva}`);
    return section.raw + (rva - section.address);
  };

  const imports = [];
  const importTable = directory(1);
  if (importTable !== 0) {
    for (let at = offsetOf(importTable); ; at += 20) {
      const lookup = read.u32(at);
      const name = read.u32(at + 12);
      const thunks = read.u32(at + 16);
      if (lookup === 0 && name === 0 && thunks === 0) break;
      imports.push(read.name(offsetOf(name)));
    }
  }
  const delayed = [];
  const delayTable = directory(13);
  if (delayTable !== 0) {
    for (let at = offsetOf(delayTable); ; at += 32) {
      const attributes = read.u32(at);
      const name = read.u32(at + 4);
      if (name === 0) break;
      // A descriptor of an old linker names its DLL by an address, not a relative one (attribute bit 0 clear).
      const rva = attributes & 1 ? name : Number(BigInt(name) - imageBase);
      delayed.push(read.name(offsetOf(rva)));
    }
  }
  return { imports, delayed };
}

function filesUnder(entry, found) {
  const stat = lstatSync(entry);
  if (stat.isDirectory()) {
    for (const name of readdirSync(entry).sort()) filesUnder(path.join(entry, name), found);
  } else if (stat.isFile()) {
    found.push(entry);
  }
  return found;
}

/**
 * The imports of the PE files under the paths that are neither a file of the package — any file under the paths, by
 * its name in any case — nor a DLL of a clean Windows; each as `<file>: <how> <DLL>`, the files by their path under the
 * path given. `checked` counts the PE files read.
 */
export function importProblems(paths) {
  const files = paths.flatMap((entry) => filesUnder(entry, []).map((file) => ({ file, shown: path.relative(lstatSync(entry).isDirectory() ? entry : path.dirname(entry), file) })));
  const packaged = new Set(files.map(({ file }) => path.basename(file).toLowerCase()));
  const problems = [];
  let checked = 0;
  for (const { file, shown } of files) {
    let found;
    try {
      found = peImports(readFileSync(file));
    } catch (error) {
      problems.push(`${shown}: ${error.message}`);
      continue;
    }
    if (!found) continue;
    checked += 1;
    for (const [how, names] of [['imports', found.imports], ['imports late', found.delayed]]) {
      for (const name of names) {
        if (packaged.has(name.toLowerCase()) || windowsDll(name)) continue;
        const why = REDISTRIBUTABLE.test(name) ? ' — a DLL of Visual C++ Redistributable, which a clean Windows has not' : ', which is neither in the package nor a DLL of a clean Windows';
        problems.push(`${shown}: ${how} ${name}${why}`);
      }
    }
  }
  return { checked, problems };
}

export function main(args = process.argv.slice(2)) {
  if (args.length === 0) throw new Error('usage: desktop-app-imports.mjs <file or directory>…');
  const { checked, problems } = importProblems(args.map((entry) => path.resolve(entry)));
  if (checked === 0) throw new Error('no PE file under the paths');
  for (const problem of problems) console.error(problem);
  if (problems.length > 0) throw new Error(`${problems.length} imports a clean Windows cannot load, in ${checked} PE files`);
  console.log(`the imports of ${checked} PE files: the package and Windows`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
