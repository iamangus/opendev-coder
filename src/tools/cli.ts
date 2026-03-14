import { spawn } from 'child_process';
import type { WorktreeJail } from '../worktree.js';

// 120-second ceiling is generous enough for builds/tests while still preventing runaway processes.
// Pass a custom timeoutMs to executeTerminalCommand for shorter-lived commands.
const DEFAULT_TIMEOUT = 120_000;

export interface CommandResult {
  stdout: string;
  stderr: string;
  exitCode: number;
  timedOut: boolean;
}

export async function executeTerminalCommand(
  jail: WorktreeJail,
  command: string,
  timeoutMs: number = DEFAULT_TIMEOUT
): Promise<CommandResult> {
  return new Promise((resolve) => {
    // detached: true creates a new process group so we can kill all children
    const proc = spawn('sh', ['-c', command], {
      cwd: jail.root,
      env: process.env,
      detached: true,
    });

    let stdout = '';
    let stderr = '';
    let timedOut = false;

    const timer = setTimeout(() => {
      timedOut = true;
      try {
        // Kill the entire process group (negative PID)
        process.kill(-(proc.pid as number), 'SIGKILL');
      } catch {
        proc.kill('SIGKILL');
      }
    }, timeoutMs);

    proc.stdout.on('data', (data: Buffer) => {
      stdout += data.toString();
    });
    proc.stderr.on('data', (data: Buffer) => {
      stderr += data.toString();
    });

    proc.on('close', (code) => {
      clearTimeout(timer);
      resolve({
        stdout,
        stderr,
        exitCode: timedOut ? -1 : (code ?? -1),
        timedOut,
      });
    });
  });
}

export async function getGitDiff(jail: WorktreeJail): Promise<string> {
  const diffResult = await executeTerminalCommand(jail, 'git diff HEAD');
  const untrackedResult = await executeTerminalCommand(
    jail,
    'git ls-files --others --exclude-standard'
  );

  const parts: string[] = [];
  if (diffResult.stdout.trim()) {
    parts.push(diffResult.stdout.trim());
  }
  if (untrackedResult.stdout.trim()) {
    parts.push(`Untracked files:\n${untrackedResult.stdout.trim()}`);
  }
  return parts.join('\n\n');
}

export { DEFAULT_TIMEOUT };
