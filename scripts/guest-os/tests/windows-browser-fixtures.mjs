function crc(bytes) { let c = -1; for (const byte of bytes) { c ^= byte; for (let j = 0; j < 8; j++) c = c >>> 1 ^ (c & 1 ? 0xedb88320 : 0); } return (c ^ -1) >>> 0; }
export function fixtureZip(entries) {
  const local = [], central = []; let offset = 0;
  for (const entry of entries) {
    const name = Buffer.from(entry.name), data = Buffer.from(entry.data || ""), flags = entry.flags ?? 0x800;
    const header = Buffer.alloc(30); header.writeUInt32LE(0x04034b50); header.writeUInt16LE(flags, 6); header.writeUInt32LE(crc(data), 14); header.writeUInt32LE(data.length, 18); header.writeUInt32LE(entry.size ?? data.length, 22); header.writeUInt16LE(name.length, 26);
    const directory = Buffer.alloc(46); directory.writeUInt32LE(0x02014b50); directory.writeUInt16LE(flags, 8); directory.writeUInt32LE(entry.crc ?? crc(data), 16); directory.writeUInt32LE(data.length, 20); directory.writeUInt32LE(entry.size ?? data.length, 24); directory.writeUInt16LE(name.length, 28); directory.writeUInt32LE((entry.attributes ?? 0) >>> 0, 38); directory.writeUInt32LE(offset, 42);
    local.push(header, name, data); central.push(directory, name); offset += header.length + name.length + data.length;
  }
  const records = Buffer.concat(central), end = Buffer.alloc(22); end.writeUInt32LE(0x06054b50); end.writeUInt16LE(entries.length, 8); end.writeUInt16LE(entries.length, 10); end.writeUInt32LE(records.length, 12); end.writeUInt32LE(offset, 16);
  return Buffer.concat([...local, records, end]);
}
