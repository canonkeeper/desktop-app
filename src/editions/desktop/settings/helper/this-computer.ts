// SPDX-License-Identifier: AGPL-3.0-only

/** One part of an address of IPv4 without a zero before it: 0…255. */
const octet = '(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)';
const ipv4 = new RegExp(`^${octet}\\.${octet}\\.${octet}\\.${octet}$`, 'u');

/** An address of IPv6 in the one form the address of a page writes it, or null when it is none. */
function ipv6(name: string) {
  try {
    const host = new URL(`http://[${name}]/`).hostname;
    return host.slice(1, -1);
  } catch {
    return null;
  }
}

/**
 * Whether a host is this computer (specs/2026-11-19-desk-trust.md TW4.1): localhost in any case or an address of the loopback
 * — 127.0.0.0/8, ::1 —, in the brackets of an address or not; an address of the home network, a name and anything else are
 * not. The rule of the server (`aisettings.LocalHost`), held to the same table
 * backend/internal/domain/aisettings/testdata/local-hosts.json.
 */
export function isThisComputer(host: string) {
  const name = host.startsWith('[') && host.endsWith(']') ? host.slice(1, -1) : host;
  if (name.toLowerCase() === 'localhost') return true;
  const four = ipv4.exec(name);
  if (four) return four[1] === '127';
  if (!name.includes(':')) return false;
  const six = ipv6(name);
  // ::1, or an address of IPv4 in IPv6 whose first part is 127 (::ffff:7f…).
  return six === '::1' || (six !== null && /^::ffff:7f[0-9a-f]{2}:[0-9a-f]{1,4}$/u.test(six));
}

/**
 * The address of «Другой, совместимый с OpenAI» goes without encryption to another computer (TW4.2): http, and its host is
 * not this computer. An address that is no address says nothing.
 */
export function plainToTheNetwork(address: string) {
  let parsed: URL;
  try {
    parsed = new URL(address.trim());
  } catch {
    return false;
  }
  return parsed.protocol === 'http:' && parsed.hostname !== '' && !isThisComputer(parsed.hostname);
}
