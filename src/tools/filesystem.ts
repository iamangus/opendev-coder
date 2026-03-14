import fs from 'fs/promises';
import { existsSync } from 'fs';
import path from 'path';
import { createReadStream } from 'fs';
import { createInterface } from 'readline';
import fg from 'fast-glob';
import type { WorktreeJail } from '../worktree.js';
import type { FileLockManager } from '../locks.js';
import { ToolError } from '../worktree.js';

// 1MB limit guards against accidentally loading huge binaries or logs into the LLM context.
// Override by adjusting this constant if your workload requires larger file reads.
const MAX_FILE_SIZE = 1_048_576;

const IGNORED_DIRS = new Set([
  '.git', 'node_modules', 'build', 'dist', '.next',
  '__pycache__', '.cache', 'coverage', 'out', 'target',
  'vendor', '.venv', 'venv'
]);

export async function readFile(
  jail: WorktreeJail,
  locks: FileLockManager,
  relativePath: string
): Promise<string> {
  const absPath = jail.resolve(relativePath);
  return locks.withRead(absPath, async () => {
    let stat;
    try {
      stat = await fs.stat(absPath);
    } catch {
      throw new ToolError(`Tool Error: File not found: ${relativePath}`);
    }
    if (stat.size > MAX_FILE_SIZE) {
      throw new ToolError(
        `Tool Error: File "${relativePath}" exceeds the maximum size of 1MB (${stat.size} bytes).`
      );
    }
    return fs.readFile(absPath, 'utf-8');
  });
}

export async function readLines(
  jail: WorktreeJail,
  locks: FileLockManager,
  relativePath: string,
  startLine: number,
  endLine: number
): Promise<string> {
  const absPath = jail.resolve(relativePath);
  return locks.withRead(absPath, async () => {
    try {
      await fs.access(absPath);
    } catch {
      throw new ToolError(`Tool Error: File not found: ${relativePath}`);
    }
    return new Promise<string>((resolve, reject) => {
      const lines: string[] = [];
      let lineNum = 0;
      const rl = createInterface({
        input: createReadStream(absPath),
        crlfDelay: Infinity,
      });
      rl.on('line', (line) => {
        lineNum++;
        if (lineNum >= startLine && lineNum <= endLine) {
          lines.push(line);
        }
        if (lineNum > endLine) {
          rl.close();
        }
      });
      rl.on('close', () => resolve(lines.join('\n')));
      rl.on('error', reject);
    });
  });
}

export async function createFile(
  jail: WorktreeJail,
  locks: FileLockManager,
  relativePath: string,
  content: string
): Promise<string> {
  const absPath = jail.resolve(relativePath);
  return locks.withWrite(absPath, async () => {
    if (existsSync(absPath)) {
      throw new ToolError(`Tool Error: File already exists: ${relativePath}. Use search_and_replace to modify existing files.`);
    }
    await fs.mkdir(path.dirname(absPath), { recursive: true });
    await fs.writeFile(absPath, content, 'utf-8');
    return `File created successfully: ${relativePath}`;
  });
}

export async function listDirectory(
  jail: WorktreeJail,
  relativePath: string,
  recursive: boolean
): Promise<string> {
  const absPath = jail.resolve(relativePath);

  let stat;
  try {
    stat = await fs.stat(absPath);
  } catch {
    throw new ToolError(`Tool Error: Directory not found: ${relativePath}`);
  }
  if (!stat.isDirectory()) {
    throw new ToolError(`Tool Error: Path is not a directory: ${relativePath}`);
  }

  const ignoredPattern = `{${[...IGNORED_DIRS].join(',')}}`;

  if (recursive) {
    const entries = await fg(['**/*'], {
      cwd: absPath,
      dot: true,
      onlyFiles: false,
      ignore: [`**/${ignoredPattern}/**`, `**/${ignoredPattern}`],
    });
    if (entries.length === 0) return '(empty directory)';
    return entries.sort().join('\n');
  } else {
    const entries = await fs.readdir(absPath, { withFileTypes: true });
    const filtered = entries.filter(e => !IGNORED_DIRS.has(e.name));
    if (filtered.length === 0) return '(empty directory)';
    return filtered
      .sort((a, b) => a.name.localeCompare(b.name))
      .map(e => e.isDirectory() ? `${e.name}/` : e.name)
      .join('\n');
  }
}

function isBinaryBuffer(buffer: Buffer): boolean {
  return buffer.includes(0);
}

export async function grepSearch(
  jail: WorktreeJail,
  pattern: string,
  directory?: string,
  isRegex?: boolean
): Promise<string> {
  const searchRoot = directory ? jail.resolve(directory) : jail.root;

  const ignoredPattern = `{${[...IGNORED_DIRS].join(',')}}`;
  const files = await fg(['**/*'], {
    cwd: searchRoot,
    dot: true,
    onlyFiles: true,
    ignore: [`**/${ignoredPattern}/**`, `**/${ignoredPattern}`],
  });

  let regex: RegExp;
  try {
    regex = isRegex ? new RegExp(pattern, 'g') : new RegExp(pattern.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'g');
  } catch {
    throw new ToolError(`Tool Error: Invalid regex pattern: ${pattern}`);
  }

  const results: string[] = [];

  for (const relFile of files.sort()) {
    const absFile = path.join(searchRoot, relFile);

    let buf: Buffer;
    try {
      buf = await fs.readFile(absFile) as Buffer;
    } catch {
      continue;
    }
    if (isBinaryBuffer(buf)) continue;

    const content = buf.toString('utf-8');
    const lines = content.split('\n');

    const matchingLineNums: number[] = [];
    for (let i = 0; i < lines.length; i++) {
      regex.lastIndex = 0;
      if (regex.test(lines[i])) {
        matchingLineNums.push(i);
      }
    }

    if (matchingLineNums.length === 0) continue;

    const displayPath = path.relative(jail.root, absFile);
    results.push(`\n${displayPath}:`);

    // group lines with context (2 before and after)
    const shown = new Set<number>();
    const groups: Array<{start: number, end: number}> = [];

    for (const lineNum of matchingLineNums) {
      const start = Math.max(0, lineNum - 2);
      const end = Math.min(lines.length - 1, lineNum + 2);
      groups.push({ start, end });
    }

    // merge overlapping groups
    const merged: Array<{start: number, end: number}> = [];
    for (const g of groups) {
      if (merged.length === 0 || g.start > merged[merged.length - 1].end + 1) {
        merged.push({ ...g });
      } else {
        merged[merged.length - 1].end = Math.max(merged[merged.length - 1].end, g.end);
      }
    }

    for (const group of merged) {
      if (shown.size > 0) results.push('  ---');
      for (let i = group.start; i <= group.end; i++) {
        const isMatch = matchingLineNums.includes(i);
        const prefix = isMatch ? '> ' : '  ';
        results.push(`  ${prefix}${i + 1}: ${lines[i]}`);
        shown.add(i);
      }
    }
  }

  if (results.length === 0) {
    return 'No matches found.';
  }
  return results.join('\n');
}

function normalizeForFuzzy(line: string): string {
  return line.trim().replace(/\s+/g, ' ');
}

function computeSimilarity(a: string, b: string): number {
  const normA = a.split('\n').map(normalizeForFuzzy).join('\n');
  const normB = b.split('\n').map(normalizeForFuzzy).join('\n');
  if (normA === normB) return 1.0;
  const maxLen = Math.max(normA.length, normB.length);
  if (maxLen === 0) return 1.0;
  let matches = 0;
  const aChars = normA.split('');
  const bChars = normB.split('');
  const minLen = Math.min(aChars.length, bChars.length);
  for (let i = 0; i < minLen; i++) {
    if (aChars[i] === bChars[i]) matches++;
  }
  return matches / maxLen;
}

export async function searchAndReplace(
  jail: WorktreeJail,
  locks: FileLockManager,
  relativePath: string,
  searchBlock: string,
  replaceBlock: string
): Promise<string> {
  const absPath = jail.resolve(relativePath);
  return locks.withWrite(absPath, async () => {
    let content: string;
    try {
      content = await fs.readFile(absPath, 'utf-8');
    } catch {
      throw new ToolError(`Tool Error: File not found: ${relativePath}`);
    }

    // Exact match
    const exactCount = content.split(searchBlock).length - 1;
    if (exactCount === 1) {
      const updated = content.replace(searchBlock, replaceBlock);
      await fs.writeFile(absPath, updated, 'utf-8');
      const idx = updated.indexOf(replaceBlock);
      const lines = updated.split('\n');
      const charsBefore = updated.substring(0, idx).split('\n').length - 1;
      const replaceLines = replaceBlock.split('\n').length;
      const start = Math.max(0, charsBefore - 2);
      const end = Math.min(lines.length, charsBefore + replaceLines + 2);
      const segment = lines.slice(start, end).join('\n');
      return `File updated successfully.\n\nUpdated segment:\n${segment}`;
    }
    if (exactCount > 1) {
      throw new ToolError(
        `Tool Error: The search block matches ${exactCount} locations in the file. Please provide more context to make the match unique.`
      );
    }

    // Fuzzy matching
    const fileLines = content.split('\n');
    const searchLines = searchBlock.split('\n');
    const windowSize = searchLines.length;

    let bestScore = 0;
    let bestStart = -1;
    let bestEnd = -1;

    for (let i = 0; i <= fileLines.length - windowSize; i++) {
      const chunk = fileLines.slice(i, i + windowSize).join('\n');
      const score = computeSimilarity(searchBlock, chunk);
      if (score > bestScore) {
        bestScore = score;
        bestStart = i;
        bestEnd = i + windowSize;
      }
    }

    if (bestScore < 0.6) {
      const bestMatch = bestStart >= 0 ? fileLines.slice(bestStart, bestEnd).join('\n') : '(no close match found)';
      throw new ToolError(
        `Tool Error: Could not find a match for the search block. Best match (similarity: ${(bestScore * 100).toFixed(1)}%):\n${bestMatch}\n\nPlease verify your search block matches the file content.`
      );
    }

    // Apply fuzzy replacement
    const beforeLines = fileLines.slice(0, bestStart);
    const afterLines = fileLines.slice(bestEnd);
    const updatedLines = [...beforeLines, ...replaceBlock.split('\n'), ...afterLines];
    const updated = updatedLines.join('\n');
    await fs.writeFile(absPath, updated, 'utf-8');

    const start = Math.max(0, bestStart - 2);
    const end = Math.min(updatedLines.length, bestStart + replaceBlock.split('\n').length + 2);
    const segment = updatedLines.slice(start, end).join('\n');
    return `File updated successfully (fuzzy match, similarity: ${(bestScore * 100).toFixed(1)}%).\n\nUpdated segment:\n${segment}`;
  });
}
