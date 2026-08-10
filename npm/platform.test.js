'use strict';

const test = require('node:test');
const assert = require('node:assert');
const { artifactFor, supportedPairs } = require('./platform');
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
