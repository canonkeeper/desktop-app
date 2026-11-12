/**
 * The entries of a zip the author gives the page — a Word chapter, an EPUB or ODT book, an FB2 in a zip — read in the
 * browser without a library: stored or deflated, by the central directory. Nothing is sent; the server reads every file
 * itself. Used to count the words of a chapter (specs/2026-11-11-ux4-after-answer.md AA4.2) and to read how a book begins
 * (specs/2026-11-11-ux9-shelf-chat.md SC1.3).
 */

/** The bytes of a file or of a part of it; an older browser reads them through FileReader. */
export function bytesOf(file: Blob): Promise<Uint8Array> {
  if (typeof file.arrayBuffer === 'function') return file.arrayBuffer().then((buffer) => new Uint8Array(buffer));
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(new Uint8Array(reader.result as ArrayBuffer));
    reader.onerror = () => reject(reader.error ?? new Error('unreadable'));
    reader.readAsArrayBuffer(file);
  });
}

export interface ZipEntry {
  name: string;
  /** The bytes of the entry; undefined when it is packed by a method the browser cannot inflate. */
  read: () => Promise<Uint8Array | undefined>;
}

const view = (bytes: Uint8Array) => new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);

/** The entries of a zip in the order of its central directory; none when the bytes are no zip. */
export function zipEntries(bytes: Uint8Array): ZipEntry[] {
  const data = view(bytes);
  // The end of the central directory: its signature within the last 64 KB and 22 bytes.
  let end = -1;
  for (let at = bytes.length - 22; at >= Math.max(0, bytes.length - 65_557); at -= 1) {
    if (data.getUint32(at, true) === 0x06054b50) { end = at; break; }
  }
  if (end < 0) return [];
  const count = data.getUint16(end + 10, true);
  let at = data.getUint32(end + 16, true);
  const decoder = new TextDecoder();
  const entries: ZipEntry[] = [];
  for (let index = 0; index < count && at + 46 <= bytes.length; index += 1) {
    if (data.getUint32(at, true) !== 0x02014b50) break;
    const method = data.getUint16(at + 10, true);
    const size = data.getUint32(at + 20, true);
    const nameLength = data.getUint16(at + 28, true);
    const skip = nameLength + data.getUint16(at + 30, true) + data.getUint16(at + 32, true);
    const local = data.getUint32(at + 42, true);
    const name = decoder.decode(bytes.subarray(at + 46, at + 46 + nameLength));
    entries.push({
      name,
      read: async () => {
        const start = local + 30 + data.getUint16(local + 26, true) + data.getUint16(local + 28, true);
        const packed = bytes.subarray(start, start + size);
        if (method === 0) return packed;
        if (method !== 8 || typeof DecompressionStream !== 'function') return undefined;
        const stream = new Response(packed.slice()).body!.pipeThrough(new DecompressionStream('deflate-raw'));
        return new Uint8Array(await new Response(stream).arrayBuffer());
      },
    });
    at += 46 + skip;
  }
  return entries;
}

/** One entry of a zip by its name, as UTF-8 text; undefined when it is not there or cannot be inflated. */
export async function zipText(bytes: Uint8Array, name: string): Promise<string | undefined> {
  const entry = zipEntries(bytes).find((each) => each.name === name);
  const read = entry ? await entry.read() : undefined;
  return read ? new TextDecoder().decode(read) : undefined;
}

const crcTable = (() => {
  const table = new Uint32Array(256);
  for (let index = 0; index < 256; index += 1) {
    let value = index;
    for (let bit = 0; bit < 8; bit += 1) value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
    table[index] = value >>> 0;
  }
  return table;
})();

function crc32(bytes: Uint8Array) {
  let crc = 0xffffffff;
  for (const byte of bytes) crc = crcTable[(crc ^ byte) & 0xff]! ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

/**
 * One zip of several files, stored as they are (specs/2026-11-11-ux9-shelf-chat.md SC1.5: «Скачать всю сагу» — the books of
 * a saga in one file): the files of the books are packed already, so nothing is deflated. Names are UTF-8.
 */
export function zipFiles(files: readonly { name: string; bytes: Uint8Array }[], at = new Date()): Uint8Array {
  const encoder = new TextEncoder();
  const time = (at.getHours() << 11) | (at.getMinutes() << 5) | Math.floor(at.getSeconds() / 2);
  const date = ((Math.max(at.getFullYear(), 1980) - 1980) << 9) | ((at.getMonth() + 1) << 5) | at.getDate();
  const locals: Uint8Array[] = [];
  const centrals: Uint8Array[] = [];
  let offset = 0;
  for (const file of files) {
    const name = encoder.encode(file.name);
    const crc = crc32(file.bytes);
    const local = new Uint8Array(30 + name.length);
    const head = new DataView(local.buffer);
    head.setUint32(0, 0x04034b50, true);
    head.setUint16(4, 20, true);
    head.setUint16(6, 0x0800, true);
    head.setUint16(10, time, true);
    head.setUint16(12, date, true);
    head.setUint32(14, crc, true);
    head.setUint32(18, file.bytes.length, true);
    head.setUint32(22, file.bytes.length, true);
    head.setUint16(26, name.length, true);
    local.set(name, 30);
    const central = new Uint8Array(46 + name.length);
    const entry = new DataView(central.buffer);
    entry.setUint32(0, 0x02014b50, true);
    entry.setUint16(4, 20, true);
    entry.setUint16(6, 20, true);
    entry.setUint16(8, 0x0800, true);
    entry.setUint16(12, time, true);
    entry.setUint16(14, date, true);
    entry.setUint32(16, crc, true);
    entry.setUint32(20, file.bytes.length, true);
    entry.setUint32(24, file.bytes.length, true);
    entry.setUint16(28, name.length, true);
    entry.setUint32(42, offset, true);
    central.set(name, 46);
    locals.push(local, file.bytes);
    centrals.push(central);
    offset += local.length + file.bytes.length;
  }
  const directory = centrals.reduce((size, central) => size + central.length, 0);
  const end = new Uint8Array(22);
  const tail = new DataView(end.buffer);
  tail.setUint32(0, 0x06054b50, true);
  tail.setUint16(8, files.length, true);
  tail.setUint16(10, files.length, true);
  tail.setUint32(12, directory, true);
  tail.setUint32(16, offset, true);
  const zip = new Uint8Array(offset + directory + 22);
  let at2 = 0;
  for (const part of [...locals, ...centrals, end]) {
    zip.set(part, at2);
    at2 += part.length;
  }
  return zip;
}
