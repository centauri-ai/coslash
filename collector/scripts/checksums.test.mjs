import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import test from "node:test";

const execFileAsync = promisify(execFile);
const script = fileURLToPath(new URL("./checksums.mjs", import.meta.url));

test("writes sorted portable SHA-256 records", async () => {
  const directory = await mkdtemp(join(tmpdir(), "coslash-checksums-"));
  const alpha = join(directory, "alpha.txt");
  const zulu = join(directory, "zulu.txt");
  const output = join(directory, "checksums.txt");
  await writeFile(alpha, "alpha\n");
  await writeFile(zulu, "zulu\n");

  await execFileAsync(process.execPath, [script, output, zulu, alpha]);

  assert.equal(
    await readFile(output, "utf8"),
    "b6a98d9ce9a2d9149288fa3df42d377c3e42737afdcdaf714e33c0a100b51060  alpha.txt\n" +
      "4c8e0c0ec12989ff67bc82a6ea812393592d126d87294021b9a469bcbd286a41  zulu.txt\n",
  );
});

test("does not replace an existing output when an input is missing", async () => {
  const directory = await mkdtemp(join(tmpdir(), "coslash-checksums-"));
  const output = join(directory, "checksums.txt");
  await writeFile(output, "previous\n");

  await assert.rejects(execFileAsync(process.execPath, [script, output, join(directory, "missing")]));
  assert.equal(await readFile(output, "utf8"), "previous\n");
});
