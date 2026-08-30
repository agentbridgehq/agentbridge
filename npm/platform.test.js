'use strict';

const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');
const fs = require('node:fs');
const { artifactFor, supportedPairs, binaryPath } = require('./platform');
const { verifyChecksum, sha256 } = require('./install');

// A wrong mapping produces a confident download of a binary for the wrong
// architecture, and the failure surfaces much later as "exec format error" with
// nothing pointing back at the cause.
test('artifact names match the release naming template', () => {
  assert.equal(
    artifactFor('1.2.0', 'darwin', 'arm64').name,
    'agentbridge_1.2.0_darwin_arm64.tar.gz'
  );
  assert.equal(
    artifactFor('1.2.0', 'linux', 'x64').name,
    'agentbridge_1.2.0_linux_amd64.tar.gz'
  );
  assert.equal(
    artifactFor('1.2.0', 'win32', 'arm64').name,
    'agentbridge_1.2.0_windows_arm64.zip'
  );
});

test('a leading v is accepted and stripped', () => {
  assert.equal(
    artifactFor('v1.2.0', 'linux', 'arm64').name,
    artifactFor('1.2.0', 'linux', 'arm64').name
  );
});

test('windows gets a .exe binary name', () => {
  assert.equal(artifactFor('1.0.0', 'win32', 'x64').binary, 'agentbridge.exe');
  assert.equal(artifactFor('1.0.0', 'linux', 'x64').binary, 'agentbridge');
});

test('unsupported platforms fail with something actionable', () => {
  assert.throws(
    () => artifactFor('1.0.0', 'sunos', 'sparc'),
    (err) => {
      assert.match(err.message, /sunos\/sparc/);
      // A dead end that does not say what is supported, or what to do
      // instead, leaves the reader stuck.
      assert.match(err.message, /Supported:/);
      assert.match(err.message, /Build from source/);
      return true;
    }
  );
});

test('every supported pair resolves', () => {
  for (const pair of supportedPairs()) {
    const [platform, arch] = pair.split('/');
    const artifact = artifactFor('1.0.0', platform, arch);
    assert.ok(artifact.name.includes(artifact.os));
    assert.ok(artifact.name.includes(artifact.arch));
  }
});

// npm postinstall scripts are a well-worn supply-chain vector. A tool that
// argues about the provenance of plugins cannot have an installer that
// downloads a binary and trusts it.
test('checksum verification accepts a listed, matching artifact', () => {
  const body = Buffer.from('binary contents');
  const checksums = `${sha256(body)}  agentbridge_1.0.0_linux_amd64.tar.gz\n`;

  assert.doesNotThrow(() =>
    verifyChecksum('agentbridge_1.0.0_linux_amd64.tar.gz', body, checksums)
  );
});

test('checksum verification rejects tampering', () => {
  const checksums = `${sha256(Buffer.from('original'))}  artifact.tar.gz\n`;

  assert.throws(
    () => verifyChecksum('artifact.tar.gz', Buffer.from('tampered'), checksums),
    /checksum mismatch/
  );
});

// The unlisted case is the one the check exists for: skipping verification
// when the entry is absent would let a substituted artifact through.
test('checksum verification rejects an artifact the file does not list', () => {
  const checksums = `${sha256(Buffer.from('x'))}  some-other-file.tar.gz\n`;

  assert.throws(
    () => verifyChecksum('artifact.tar.gz', Buffer.from('x'), checksums),
    /does not list/
  );
});

// The downloaded binary must not land on the shim.
//
// It did. bin/agentbridge is the shim npm links onto the PATH and is shipped in
// the package; the installer downloaded the real binary to that same name. So
// the installer's "already present?" check saw the shim and skipped the
// download, and the shim then found "the binary" — itself — and spawned it,
// recursing until it was killed. `npm i -g @agentbridgehq/agentbridge` installed a command
// that hung the first time anyone ran it, and nothing failed loudly enough to
// notice: the install printed success.
test('the downloaded binary does not collide with the shim npm puts on the PATH', () => {
  const root = '/pkg';
  const shim = path.join(root, 'bin', 'agentbridge');

  assert.notEqual(binaryPath(root, 'darwin'), shim);
  assert.notEqual(binaryPath(root, 'linux'), shim);
  assert.notEqual(binaryPath(root, 'win32'), path.join(root, 'bin', 'agentbridge.exe'));

  // Nor anywhere else in bin/, which npm links wholesale.
  for (const platform of ['darwin', 'linux', 'win32']) {
    assert.notEqual(
      path.dirname(binaryPath(root, platform)),
      path.join(root, 'bin'),
      `${platform}: the binary must not be downloaded into bin/`
    );
  }
});

// The shim resolves the binary through platform.js rather than computing the
// path itself, which is what stops the two from drifting apart again.
test('the shim asks platform.js where the binary is', () => {
  const shim = fs.readFileSync(path.join(__dirname, 'bin', 'agentbridge'), 'utf8');
  assert.match(shim, /binaryPath\(/, 'the shim must use platform.js binaryPath()');
});
