'use strict';

// Downloads the agentbridge binary for this platform.
//
// The package ships no binaries. Publishing six platforms' worth would make the
// tarball enormous for every user, and the alternative — optional
// platform-specific packages — multiplies the number of things that must be
// published in lockstep. Fetching one at install time keeps the package small
// and the release process single-artifact.
//
// The checksum is verified before anything is written. npm postinstall scripts
// are a well-worn supply-chain vector, and a tool that argues about the
// provenance of plugins cannot have an installer that downloads a binary and
// trusts it.

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const crypto = require('crypto');
const { execFileSync } = require('child_process');
const { artifactFor, binaryPath } = require('./platform');

const REPO = 'agentbridgehq/agentbridge';
const VERSION = require('./package.json').version;
const BASE =
  process.env.AGENTBRIDGE_BASE_URL ||
  `https://github.com/${REPO}/releases/download/v${VERSION}`;

async function fetchBuffer(url) {
  const res = await fetch(url, { redirect: 'follow' });
  if (!res.ok) {
    throw new Error(`${url}: HTTP ${res.status}`);
  }
  return Buffer.from(await res.arrayBuffer());
}

function sha256(buf) {
  return crypto.createHash('sha256').update(buf).digest('hex');
}

// verifyChecksum refuses anything the checksums file does not list, rather than
// skipping verification when the entry is absent — an unlisted artifact is the
// case this check exists to catch.
function verifyChecksum(name, buf, checksums) {
  const line = checksums
    .split('\n')
    .map((l) => l.trim())
    .find((l) => l.endsWith(` ${name}`) || l.endsWith(`  ${name}`));

  if (!line) {
    throw new Error(`checksums.txt does not list ${name}; refusing to install`);
  }

  const expected = line.split(/\s+/)[0];
  const actual = sha256(buf);
  if (expected !== actual) {
    throw new Error(
      `checksum mismatch for ${name}\n  expected ${expected}\n  actual   ${actual}\nDo not use this download.`
    );
  }
}

// extract pulls the single binary out of the archive.
//
// tar and unzip are invoked rather than depended on: adding an archive library
// would put third-party code in the install path of a security tool for the
// sake of one file.
function extract(archivePath, artifact, destDir) {
  if (archivePath.endsWith('.zip')) {
    execFileSync('unzip', ['-o', '-q', archivePath, artifact.binary, '-d', destDir], {
      stdio: 'inherit',
    });
    return;
  }
  execFileSync('tar', ['-xzf', archivePath, '-C', destDir, artifact.binary], {
    stdio: 'inherit',
  });
}

async function main() {
  const artifact = artifactFor(VERSION, process.platform, process.arch);
  const target = binaryPath(__dirname, process.platform);
  const destDir = path.dirname(target);

  if (fs.existsSync(target)) {
    return;
  }

  process.stderr.write(`agentbridge v${VERSION} (${artifact.os}/${artifact.arch})\n`);

  const [archive, checksums] = await Promise.all([
    fetchBuffer(`${BASE}/${artifact.name}`),
    fetchBuffer(`${BASE}/checksums.txt`).then((b) => b.toString('utf8')),
  ]);

  verifyChecksum(artifact.name, archive, checksums);
  process.stderr.write('  checksum  ok\n');

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'agentbridge-'));
  try {
    const archivePath = path.join(tmp, artifact.name);
    fs.writeFileSync(archivePath, archive);

    fs.mkdirSync(destDir, { recursive: true });
    extract(archivePath, artifact, destDir);
    fs.chmodSync(target, 0o755);

    process.stderr.write(`  installed ${target}\n`);
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

main().catch((err) => {
  process.stderr.write(`\nagentbridge install failed: ${err.message}\n\n`);
  process.stderr.write(
    'Install another way instead:\n' +
      '  brew install agentbridge/tap/agentbridge\n' +
      `  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | sh\n`
  );
  process.exit(1);
});

module.exports = { verifyChecksum, sha256 };
