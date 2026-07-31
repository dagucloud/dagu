/**
 * Copyright (C) 2026 Yota Hamada
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

const path = require("path");
const fs = require("fs");

// Platform mapping from Node.js to npm package names
const PLATFORM_MAP = {
  // Tier 1 - Most common platforms
  "linux-x64": "dagu-linux-x64",
  "linux-arm64": "dagu-linux-arm64",
  "darwin-x64": "dagu-darwin-x64",
  "darwin-arm64": "dagu-darwin-arm64",
  "win32-x64": "dagu-win32-x64",

  // Tier 2 - Common but less frequent
  "linux-ia32": "dagu-linux-ia32",
  "win32-ia32": "dagu-win32-ia32",

  // Tier 3 - Rare platforms
  "win32-arm64": "dagu-win32-arm64",
  "linux-ppc64": "dagu-linux-ppc64",
  "linux-s390x": "dagu-linux-s390x",
};

// Cache for binary path
let cachedBinaryPath = null;

/**
 * Detect ARM variant on Linux systems
 * @returns {string} ARM variant ('6' or '7')
 */
function getArmVariant() {
  // First try process.config
  if (
    process.config &&
    process.config.variables &&
    process.config.variables.arm_version
  ) {
    return String(process.config.variables.arm_version);
  }

  // On Linux, check /proc/cpuinfo
  if (process.platform === "linux") {
    try {
      const cpuinfo = fs.readFileSync("/proc/cpuinfo", "utf8");

      // Check for specific ARM architecture indicators
      if (
        cpuinfo.includes("ARMv6") ||
        cpuinfo.includes("ARM926") ||
        cpuinfo.includes("ARM1176")
      ) {
        return "6";
      }
      if (cpuinfo.includes("ARMv7") || cpuinfo.includes("Cortex-A")) {
        return "7";
      }

      // Check CPU architecture field
      const archMatch = cpuinfo.match(/^CPU architecture:\s*(\d+)/m);
      if (archMatch && archMatch[1]) {
        const arch = parseInt(archMatch[1], 10);
        if (arch >= 7) return "7";
        if (arch === 6) return "6";
      }
    } catch (e) {
      // Ignore errors, fall through to default
    }
  }

  // Default to ARMv7 (more common)
  return "7";
}

/**
 * Get the npm package name for the current platform
 * @returns {string|null} Package name or null if unsupported
 */
function getPlatformPackage() {
  let platform = process.platform;
  let arch = process.arch;

  // Special handling for ARM on Linux
  if (platform === "linux" && arch === "arm") {
    const variant = getArmVariant();
    return `dagu-linux-armv${variant}`;
  }

  const key = `${platform}-${arch}`;
  return PLATFORM_MAP[key] || null;
}

/**
 * Get the path to the Dagu binary
 * @returns {string|null} Path to binary or null if not found
 */
function getBinaryPath() {
  // Return cached path if available
  if (cachedBinaryPath && fs.existsSync(cachedBinaryPath)) {
    return cachedBinaryPath;
  }

  const binaryName = process.platform === "win32" ? "dagu.exe" : "dagu";

  // First, try platform-specific package
  const platformPackage = getPlatformPackage();
  if (platformPackage) {
    try {
      // Try to resolve the binary using require.resolve (Sentry approach)
      const binaryPath = require.resolve(
        `@dagucloud/${platformPackage}/bin/${binaryName}`
      );
      if (fs.existsSync(binaryPath)) {
        cachedBinaryPath = binaryPath;
        return binaryPath;
      }
    } catch (e) {
      // Package not installed or binary not found
    }
  }

  // Fallback to local binary in main package
  const localBinary = path.join(__dirname, "..", binaryName);
  if (fs.existsSync(localBinary)) {
    cachedBinaryPath = localBinary;
    return localBinary;
  }

  return null;
}

/**
 * Set the cached binary path
 * @param {string} binaryPath Path to the binary
 */
function setPlatformBinary(binaryPath) {
  cachedBinaryPath = binaryPath;
}

/**
 * Check if platform-specific package is installed
 * @returns {boolean} True if installed, false otherwise
 */
function isPlatformSpecificPackageInstalled() {
  const platformPackage = getPlatformPackage();
  if (!platformPackage) {
    return false;
  }

  const binaryName = process.platform === "win32" ? "dagu.exe" : "dagu";

  try {
    // Resolving will fail if the optionalDependency was not installed
    require.resolve(`@dagucloud/${platformPackage}/bin/${binaryName}`);
    return true;
  } catch (e) {
    return false;
  }
}

module.exports = {
  getPlatformPackage,
  getBinaryPath,
  setPlatformBinary,
  isPlatformSpecificPackageInstalled,
  getPlatformInfo: () => ({
    platform: process.platform,
    arch: process.arch,
    nodeVersion: process.version,
    detectedPackage: getPlatformPackage(),
  }),
};
