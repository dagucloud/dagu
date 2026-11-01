#!/usr/bin/env node
/**
 * Copyright (C) 2026 Yota Hamada
 * SPDX-License-Identifier: GPL-3.0-or-later
 */


const fs = require("fs");
const path = require("path");
const zlib = require("zlib");
const https = require("https");

const { getPlatformPackage, isPlatformSpecificPackageInstalled } = require("./lib/platform");

// Windows binaries end with .exe so we need to special case them.
const binaryName = process.platform === "win32" ? "dagu.exe" : "dagu";

// Adjust the version you want to install. You can also make this dynamic.
const PACKAGE_VERSION = require("./package.json").version;

// Compute the path we want to emit the fallback binary to
const fallbackBinaryPath = path.join(__dirname, binaryName);

function makeRequest(url) {
  return new Promise((resolve, reject) => {
    https
      .get(url, (response) => {
        if (response.statusCode >= 200 && response.statusCode < 300) {
          const chunks = [];
          response.on("data", (chunk) => chunks.push(chunk));
          response.on("end", () => {
            resolve(Buffer.concat(chunks));
          });
        } else if (
          response.statusCode >= 300 &&
          response.statusCode < 400 &&
          response.headers.location
        ) {
          // Follow redirects
          makeRequest(response.headers.location).then(resolve, reject);
        } else {
          reject(
            new Error(
              `npm responded with status code ${response.statusCode} when downloading the package!`
            )
          );
        }
      })
      .on("error", (error) => {
        reject(error);
      });
  });
}

function extractFileFromTarball(tarballBuffer, filepath) {
  // Tar archives are organized in 512 byte blocks.
  // Blocks can either be header blocks or data blocks.
  // Header blocks contain file names of the archive in the first 100 bytes, terminated by a null byte.
  // The size of a file is contained in bytes 124-135 of a header block and in octal format.
  // The following blocks will be data blocks containing the file.
  let offset = 0;
  while (offset < tarballBuffer.length) {
    const header = tarballBuffer.subarray(offset, offset + 512);
    offset += 512;

    const fileName = header.toString("utf-8", 0, 100).replace(/\0.*/g, "");
    const fileSize = parseInt(
      header.toString("utf-8", 124, 136).replace(/\0.*/g, ""),
      8
    );

    if (fileName === filepath) {
      return tarballBuffer.subarray(offset, offset + fileSize);
    }

    // Clamp offset to the uppoer multiple of 512
    offset = (offset + fileSize + 511) & ~511;
  }
}

async function downloadBinaryFromNpm() {
  // Determine package name for this platform
  const platformSpecificPackageName = getPlatformPackage();
  if (!platformSpecificPackageName) {
    throw new Error(`Unsupported platform: ${process.platform}/${process.arch}`);
  }

  const url = `https://registry.npmjs.org/@dagucloud/${platformSpecificPackageName}/-/${platformSpecificPackageName}-${PACKAGE_VERSION}.tgz`;
  console.log(`Downloading binary distribution package from ${url}...`);
  // Download the tarball of the right binary distribution package
  const tarballDownloadBuffer = await makeRequest(url);
  const tarballBuffer = zlib.unzipSync(tarballDownloadBuffer);

  // Extract binary from package and write to disk
  fs.writeFileSync(
    fallbackBinaryPath,
    extractFileFromTarball(tarballBuffer, `package/bin/${binaryName}`),
    { mode: 0o755 } // Make binary file executable
  );
}

// Skip downloading the binary if it was already installed via optionalDependencies
if (!isPlatformSpecificPackageInstalled()) {
  console.log(
    "Platform specific package not found. Will manually download binary."
  );
  downloadBinaryFromNpm().catch((error) => {
    console.error(`Failed to install the Dagu binary: ${error.message}`);
    process.exitCode = 1;
  });
} else {
  console.log(
    "Platform specific package already installed. Skipping manual binary download."
  );
}
