'use strict';

// Mapping from Node's platform names to the release artifacts.
//
// Separated from the installer so it can be tested without a network, because
// this is where a distribution bug hides best: a wrong mapping produces a
// confident download of a binary for the wrong architecture, and the failure
// surfaces much later as "exec format error" with nothing pointing back here.

const PLATFORMS = {
  darwin: 'darwin',
  linux: 'linux',
  win32: 'windows',
};

const ARCHS = {
  x64: 'amd64',
  arm64: 'arm64',
};

/**
 * Resolve the release artifact for a platform and architecture.
 *
 * @param {string} version release version, with or without a leading "v"
 * @param {string} platform Node's process.platform
 * @param {string} arch Node's process.arch
 */
function artifactFor(version, platform, arch) {
  const os = PLATFORMS[platform];
  const goarch = ARCHS[arch];

  if (!os || !goarch) {
    // Naming both the unsupported pair and the supported set turns a dead end
    // into something the reader can act on.
    throw new Error(
      `agentbridge does not publish a binary for ${platform}/${arch}.\n` +
        `Supported: ${supportedPairs().join(', ')}.\n` +
        `Build from source instead: https://github.com/agentbridgehq/agentbridge`
    );
  }

  const stripped = String(version).replace(/^v/, '');
  const ext = os === 'windows' ? 'zip' : 'tar.gz';

  return {
    os,
    arch: goarch,
    // Must match archives.name_template in .goreleaser.yaml. A drift test in
    // the Go suite keeps the two from separating.
    name: `agentbridge_${stripped}_${os}_${goarch}.${ext}`,
    binary: os === 'windows' ? 'agentbridge.exe' : 'agentbridge',
  };
}

function supportedPairs() {
  const out = [];
  for (const platform of Object.keys(PLATFORMS)) {
    for (const arch of Object.keys(ARCHS)) {
      out.push(`${platform}/${arch}`);
    }
  }
  return out;
}

module.exports = { artifactFor, supportedPairs, PLATFORMS, ARCHS };
