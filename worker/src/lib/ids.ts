const ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';

function base32StdNoPad(bytes: Uint8Array): string {
  let bits = 0;
  let value = 0;
  let out = '';
  for (const b of bytes) {
    value = (value << 8) | b;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      out += ALPHABET[(value >>> bits) & 0x1f];
    }
  }
  if (bits > 0) {
    out += ALPHABET[(value << (5 - bits)) & 0x1f];
  }
  return out;
}

export const PrefixCompany = 'co_';
export const PrefixList = 'l_';
export const PrefixContact = 'c_';
export const PrefixSend = 's_';
export const PrefixBatch = 'b_';
export const PrefixUnsub = 'u_';

export function newID(prefix: string): string {
  const b = new Uint8Array(10);
  crypto.getRandomValues(b);
  return prefix + base32StdNoPad(b).slice(0, 10);
}

export const newCompany = () => newID(PrefixCompany);
export const newList = () => newID(PrefixList);
export const newContact = () => newID(PrefixContact);
export const newSend = () => newID(PrefixSend);
export const newBatch = () => newID(PrefixBatch);
export const newUnsub = () => newID(PrefixUnsub);
