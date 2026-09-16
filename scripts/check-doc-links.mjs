import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import {
  dirname,
  extname,
  isAbsolute,
  relative,
  resolve,
  sep,
} from "node:path";

const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
  encoding: "utf8",
}).trim();
const files = execFileSync("git", ["ls-files", "-z", "--", "*.md"], {
  cwd: root,
  encoding: "utf8",
})
  .split("\0")
  .filter(Boolean);

const anchorCache = new Map();
const failures = [];

function anchorsFor(file) {
  if (anchorCache.has(file)) return anchorCache.get(file);

  const anchors = new Set();
  const counts = new Map();
  const content = readFileSync(file, "utf8");

  for (const match of content.matchAll(/^#{1,6}\s+(.+?)\s*#*\s*$/gm)) {
    const base = match[1]
      .replace(/<[^>]*>/g, "")
      .replace(/\[([^\]]+)]\([^)]+\)/g, "$1")
      .replace(/[`*_~]/g, "")
      .toLowerCase()
      .trim()
      .replace(/[^\p{L}\p{N}\s_-]/gu, "")
      .replace(/\s+/g, "-");
    const count = counts.get(base) ?? 0;
    counts.set(base, count + 1);
    anchors.add(count === 0 ? base : `${base}-${count}`);
  }

  for (const match of content.matchAll(/\s(?:id|name)=["']([^"']+)["']/g)) {
    anchors.add(match[1]);
  }

  anchorCache.set(file, anchors);
  return anchors;
}

function checkLink(source, rawLink) {
  const link = rawLink.trim();
  if (
    link === "" ||
    /^(?:[a-z][a-z\d+.-]*:|\/\/)/i.test(link) ||
    link.startsWith("/")
  ) {
    return;
  }

  const hash = link.indexOf("#");
  const rawPath = (hash === -1 ? link : link.slice(0, hash)).split("?", 1)[0];
  const rawAnchor = hash === -1 ? "" : link.slice(hash + 1);

  let path;
  let anchor;
  try {
    path = decodeURIComponent(rawPath);
    anchor = decodeURIComponent(rawAnchor);
  } catch {
    failures.push(`${source}: invalid URL encoding in ${link}`);
    return;
  }

  const target =
    path === "" ? resolve(root, source) : resolve(root, dirname(source), path);
  const relativeTarget = relative(root, target);
  if (
    relativeTarget === ".." ||
    relativeTarget.startsWith(`..${sep}`) ||
    isAbsolute(relativeTarget)
  ) {
    failures.push(`${source}: target escapes repository ${link}`);
    return;
  }
  if (!existsSync(target)) {
    failures.push(`${source}: missing target ${link}`);
    return;
  }
  if (
    anchor !== "" &&
    extname(target).toLowerCase() === ".md" &&
    !anchorsFor(target).has(anchor)
  ) {
    failures.push(`${source}: missing anchor ${link}`);
  }
}

function linksFromInlineMarkdown(content) {
  const links = [];
  const escapedPunctuationAt = (position) => {
    const character = content[position + 1];
    return character !== undefined && /[!-/:-@[-`{-~]/.test(character)
      ? character
      : null;
  };

  for (const match of content.matchAll(/!?\[[^\]]*]\(\s*/g)) {
    let position = match.index + match[0].length;
    let link = "";

    if (content[position] === "<") {
      position++;
      while (position < content.length) {
        const character = content[position];
        const escaped = escapedPunctuationAt(position);
        if (character === "\\" && escaped !== null) {
          link += escaped;
          position += 2;
        } else if (character === ">") {
          links.push(link);
          break;
        } else if (character === "\n") {
          break;
        } else {
          link += character;
          position++;
        }
      }
      continue;
    }

    let depth = 0;
    while (position < content.length) {
      const character = content[position];
      const escaped = escapedPunctuationAt(position);
      if (character === "\\" && escaped !== null) {
        link += escaped;
        position += 2;
      } else if (character === "(") {
        depth++;
        link += character;
        position++;
      } else if (character === ")") {
        if (depth === 0) {
          links.push(link);
          break;
        }
        depth--;
        link += character;
        position++;
      } else if (/\s/.test(character) && depth === 0) {
        links.push(link);
        break;
      } else {
        link += character;
        position++;
      }
    }
  }

  return links;
}

function linksFromSrcset(srcset) {
  const links = [];
  let position = 0;

  while (position < srcset.length) {
    while (/[,\t\n\f\r ]/.test(srcset[position] ?? "")) position++;
    const start = position;
    while (position < srcset.length && !/[\t\n\f\r ]/.test(srcset[position])) {
      position++;
    }
    let link = srcset.slice(start, position);
    if (link === "") break;

    if (link.endsWith(",")) {
      link = link.replace(/,+$/, "");
      if (link !== "") links.push(link);
      continue;
    }

    links.push(link);
    while (position < srcset.length && srcset[position] !== ",") position++;
  }

  return links;
}

for (const source of files) {
  const content = readFileSync(resolve(root, source), "utf8");
  const links = linksFromInlineMarkdown(content);
  for (const match of content.matchAll(
    /^\s*\[[^\]]+]:\s*(?:<([^>]+)>|(\S+))/gm,
  )) {
    links.push(match[1] ?? match[2]);
  }
  for (const match of content.matchAll(/(?:href|src)=["']([^"']+)["']/gi)) {
    links.push(match[1]);
  }
  for (const match of content.matchAll(/srcset=["']([^"']+)["']/gi)) {
    links.push(...linksFromSrcset(match[1]));
  }
  for (const link of links) checkLink(source, link);
}

if (failures.length > 0) {
  console.error(failures.join("\n"));
  process.exitCode = 1;
} else {
  console.log(`Checked ${files.length} Markdown files.`);
}
