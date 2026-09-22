import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { rename, rm, stat, writeFile } from "node:fs/promises";
import { basename, resolve } from "node:path";

async function sha256(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) {
    hash.update(chunk);
  }
  return hash.digest("hex");
}

async function main() {
  const [outputArg, ...inputArgs] = process.argv.slice(2);
  if (!outputArg || inputArgs.length === 0) {
    throw new Error("usage: node scripts/checksums.mjs OUTPUT FILE...");
  }

  const output = resolve(outputArg);
  const inputs = inputArgs.map((path) => ({ path: resolve(path), name: basename(path) }));
  inputs.sort((left, right) => (left.name < right.name ? -1 : left.name > right.name ? 1 : 0));
  if (new Set(inputs.map(({ name }) => name)).size !== inputs.length) {
    throw new Error("input file names must be unique");
  }

  const lines = [];
  for (const input of inputs) {
    const details = await stat(input.path);
    if (!details.isFile()) {
      throw new Error(`${input.path} is not a regular file`);
    }
    lines.push(`${await sha256(input.path)}  ${input.name}`);
  }

  const temporary = `${output}.tmp-${process.pid}`;
  try {
    await writeFile(temporary, `${lines.join("\n")}\n`, { flag: "wx" });
    await rm(output, { force: true });
    await rename(temporary, output);
  } finally {
    await rm(temporary, { force: true });
  }
}

await main();
