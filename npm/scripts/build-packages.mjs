#!/usr/bin/env node
// Assemble the per-platform npm packages from the release's own archives,
// so npm installs exactly the binary the GitHub release and Homebrew ship.
//
//   node npm/scripts/build-packages.mjs <version> [distDir] [outDir]
//
// For each supported platform it creates @klarlabs-studio/tokenops-<os>-<arch> holding
// one prebuilt binary plus os/cpu fields, so `npm install` fetches only the
// matching package. It also rewrites the main package's version + the
// optionalDependencies to the release version. Node built-ins only.
import { existsSync, mkdirSync, readdirSync, copyFileSync, chmodSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const npmRoot = join(here, "..");

const version = process.argv[2];
const distDir = process.argv[3] || join(npmRoot, "..", "dist");
const outDir = process.argv[4] || join(npmRoot, "packages");

if (!version) {
  console.error("usage: build-packages.mjs <version> [distDir] [outDir]");
  process.exit(2);
}

// (goos, goarch) -> (npm platform, npm arch). Matches process.platform/arch.
const TARGETS = [
  { goos: "linux", goarch: "amd64", platform: "linux", arch: "x64" },
  { goos: "linux", goarch: "arm64", platform: "linux", arch: "arm64" },
  { goos: "darwin", goarch: "amd64", platform: "darwin", arch: "x64" },
  { goos: "darwin", goarch: "arm64", platform: "darwin", arch: "arm64" },
];

// findBinary locates a target's binary. The release workflow unpacks each
// archive tokenops_<version>_<goos>_<goarch>.tar.gz into
// <distDir>/tokenops_<goos>_<goarch>/; match by prefix all the same.
function findBinary(t) {
  const prefix = `tokenops_${t.goos}_${t.goarch}`;
  const dirs = readdirSync(distDir, { withFileTypes: true })
    .filter((d) => d.isDirectory() && d.name.startsWith(prefix))
    .map((d) => d.name);
  const exe = t.goos === "windows" ? "tokenops.exe" : "tokenops";
  for (const d of dirs) {
    const p = join(distDir, d, exe);
    if (existsSync(p)) return p;
  }
  return null;
}

rmSync(outDir, { recursive: true, force: true });
mkdirSync(outDir, { recursive: true });

// Every target ships every release. A missing binary means the release is
// incomplete, and publishing the rest would leave npx broken on that
// platform while the job reports success, so fail before writing anything.
const missing = TARGETS.filter((t) => !findBinary(t));
if (missing.length > 0) {
  for (const t of missing) {
    console.error(`missing ${t.platform}-${t.arch}: no tokenops_${t.goos}_${t.goarch}*/tokenops under ${distDir}`);
  }
  process.exit(1);
}

const built = [];
for (const t of TARGETS) {
  const src = findBinary(t);
  const name = `@klarlabs-studio/tokenops-${t.platform}-${t.arch}`;
  const pkgDir = join(outDir, `tokenops-${t.platform}-${t.arch}`);
  const binDir = join(pkgDir, "bin");
  mkdirSync(binDir, { recursive: true });

  const exe = t.goos === "windows" ? "tokenops.exe" : "tokenops";
  const dst = join(binDir, exe);
  copyFileSync(src, dst);
  chmodSync(dst, 0o755);

  writeFileSync(
    join(pkgDir, "package.json"),
    JSON.stringify(
      {
        name,
        version,
        description: `tokenops binary for ${t.platform}-${t.arch}`,
        // repository.url must match the GitHub repo exactly, or npm's OIDC
        // trusted-publishing validation can't match the package to its
        // configured trusted publisher and publish fails with ENEEDAUTH. The
        // wrapper already carries this; the per-platform packages did not, which
        // is why every per-platform publish ENEEDAUTH'd first in the loop.
        repository: {
          type: "git",
          url: "git+https://github.com/klarlabs-studio/tokenops.git",
        },
        os: [t.platform],
        cpu: [t.arch],
        files: [`bin/${exe}`],
        license: "Apache-2.0",
      },
      null,
      2,
    ) + "\n",
  );
  built.push(name);
  console.log(`built ${name}`);
}

// Rewrite the main package version + optionalDependencies to this release.
const mainPath = join(npmRoot, "package.json");
const main = JSON.parse(readFileSync(mainPath, "utf8"));
main.version = version;
main.optionalDependencies = Object.fromEntries(built.map((n) => [n, version]));
writeFileSync(mainPath, JSON.stringify(main, null, 2) + "\n");

console.log(`\n${built.length} platform packages -> ${outDir}`);
console.log(`main package -> version ${version}, ${built.length} optionalDependencies`);
