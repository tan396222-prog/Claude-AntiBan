// Inspect presence only. Never print or persist credential values.
const required = [
  'CAGE_MACOS_P12_BASE64', 'CAGE_MACOS_P12_PASSWORD', 'CAGE_MACOS_SIGNING_IDENTITY',
  'CAGE_MACOS_NOTARY_KEY_BASE64', 'CAGE_MACOS_NOTARY_KEY_ID', 'CAGE_MACOS_NOTARY_ISSUER_ID',
  'CAGE_WINDOWS_QEMU_SOURCE_URL', 'CAGE_WINDOWS_QEMU_SOURCE_SHA256',
];
const provider = process.env.CAGE_WINDOWS_SIGNING_PROVIDER || 'certificate-store';
if (provider === 'esigner') {
  required.push('CAGE_ESIGNER_USERNAME', 'CAGE_ESIGNER_PASSWORD', 'CAGE_ESIGNER_CREDENTIAL_ID', 'CAGE_ESIGNER_CERTIFICATE_SERIAL', 'CAGE_ESIGNER_TOTP_SECRET');
} else if (provider === 'certificate-store') {
  required.push('CAGE_WINDOWS_PFX_BASE64', 'CAGE_WINDOWS_PFX_PASSWORD');
} else {
  throw new Error('Unknown CAGE_WINDOWS_SIGNING_PROVIDER; use esigner or certificate-store.');
}
const missing = required.filter(name => !process.env[name]?.trim());
if (missing.length) {
  console.error(`Configure these cage-release environment secrets/variables before requesting a release:\n${missing.join('\n')}`);
  process.exitCode = 1;
} else {
  if (provider === 'esigner' && (!/^[0-9a-f-]{36}$/i.test(process.env.CAGE_ESIGNER_CREDENTIAL_ID) ||
    !/^[0-9a-f]{32,40}$/i.test(process.env.CAGE_ESIGNER_CERTIFICATE_SERIAL))) {
    throw new Error('Invalid eSigner credential ID or expected certificate serial.');
  }
  let source;
  try { source = new URL(process.env.CAGE_WINDOWS_QEMU_SOURCE_URL); }
  catch { throw new Error('Reviewed QEMU source requires a valid public HTTPS URL.'); }
  if (source.protocol !== 'https:' || source.username || source.password || source.search || source.hash || !/^[0-9a-f]{64}$/i.test(process.env.CAGE_WINDOWS_QEMU_SOURCE_SHA256)) {
    throw new Error('Reviewed QEMU source requires a credential-free HTTPS URL and SHA-256.');
  }
  if (!process.env.CAGE_MACOS_SIGNING_IDENTITY.startsWith('Developer ID Application:')) throw new Error('macOS releases require a Developer ID Application identity.');
  console.log('Release configuration is present. Platform jobs will verify the credentials, signatures and corresponding sources.');
}
