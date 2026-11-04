// Writes the notices of the packages bundled in the web UI (the production
// dependencies of package-lock.json, type-only packages excepted) into
// dist/licenses.txt: their license files, fonts included. The release
// appends it to the third-party notices of the binary
// (internal/tools/licenses). It fails when a package has no license file.
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const root = new URL("..", import.meta.url).pathname;
const lock = JSON.parse(readFileSync(join(root, "package-lock.json"), "utf8"));
const noticeFile = /^(licen[cs]e|copying|notice|ofl)([.-][a-z0-9.-]*)?$/i;
const rule = "=".repeat(78);

const packages = Object.entries(lock.packages)
  .filter(([path, p]) => path.startsWith("node_modules/") && !p.dev)
  .map(([path, p]) => ({ name: path.slice("node_modules/".length), version: p.version, dir: join(root, path) }))
  .filter((p) => !p.name.startsWith("@types/") && p.name !== "csstype")
  .sort((a, b) => a.name.localeCompare(b.name));

let out = "Packages bundled in the web UI\n";
for (const p of packages) {
  if (!existsSync(p.dir)) throw new Error(`${p.name}: not installed (run npm ci)`);
  const files = readdirSync(p.dir).filter((f) => noticeFile.test(f)).sort();
  if (files.length === 0) throw new Error(`${p.name} ${p.version}: no license file in ${p.dir}`);
  for (const f of files) {
    out += `\n${rule}\n${p.name} ${p.version}: ${f}\n${rule}\n\n${readFileSync(join(p.dir, f), "utf8").trim()}\n`;
  }
}
writeFileSync(join(root, "dist", "licenses.txt"), out);
