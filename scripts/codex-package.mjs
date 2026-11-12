import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readdirSync, renameSync, rmSync, statSync } from 'node:fs';
import { join } from 'node:path';

// The Codex package of one environment, kept per version (specs/2026-10-28-codex-0165.md UC5):
// <state>/codex-packages/<version> is made once from ~/codex-bin and never rewritten, so a release
// and its rollback each find the package of their own go.mod. Only Node built-ins: the VPS runs it.

// The chat runs `codex app-server`; code mode runs the model's scripts in codex-code-mode-host.
export const requiredCodexFiles = ['codex', 'codex-code-mode-host'];

/** The Codex version pinned by backend/go.mod: the require label of the SDK. */
export function codexPackageVersion(goMod) {
  return /^\s*github\.com\/openai\/codex\/sdk\/go v(\d+\.\d+\.\d+)\s*$/m.exec(goMod)?.[1];
}

export function codexRuntimeVersionsMatch(goMod, cliOutput) {
  const sdk = codexPackageVersion(goMod);
  const cli = /^codex-cli (\d+\.\d+\.\d+)$/.exec(cliOutput.trim())?.[1];
  return sdk !== undefined && sdk === cli;
}

/** `codex --version` of the package in directory. */
export function codexCliVersion(directory) {
  const result = spawnSync(join(directory, 'codex'), ['--version'], {
    encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'], windowsHide: true,
  });
  if (result.error || result.status !== 0) throw new Error(`Codex version check failed: ${directory}`);
  return result.stdout;
}

/**
 * Finds the package of the go.mod version in packagesDir, or copies it there once from sourceDir:
 * only the regular top-level files (the legacy* directories of ~/codex-bin stay), through
 * <version>.next and one rename, so the directory of a version is whole or absent.
 */
export function installCodexPackage({ goMod, sourceDir, packagesDir }, versionOf = codexCliVersion) {
  const version = codexPackageVersion(goMod);
  if (!version) throw new Error('backend/go.mod pins no Codex SDK version');
  const target = join(packagesDir, version);
  if (existsSync(target)) {
    requireCodexPackage(target, goMod, versionOf);
    return target;
  }
  if (!isFile(join(sourceDir, 'codex'))) {
    throw new Error(`Codex ${version} package is missing: put codex-cli ${version} into ${sourceDir}`);
  }
  requireCodexPackage(sourceDir, goMod, versionOf);
  const next = `${target}.next`;
  rmSync(next, { force: true, recursive: true });
  mkdirSync(next, { recursive: true, mode: 0o700 });
  for (const entry of readdirSync(sourceDir)) {
    if (isFile(join(sourceDir, entry))) {
      cpSync(join(sourceDir, entry), join(next, entry), { dereference: true, preserveTimestamps: true });
    }
  }
  requireCodexPackage(next, goMod, versionOf);
  renameSync(next, target);
  return target;
}

function requireCodexPackage(directory, goMod, versionOf) {
  const missing = requiredCodexFiles.filter((name) => !isFile(join(directory, name)));
  if (missing.length > 0) throw new Error(`Codex package ${directory} lacks ${missing.join(', ')}`);
  if (!codexRuntimeVersionsMatch(goMod, versionOf(directory))) {
    throw new Error(`Codex CLI and Go SDK versions differ in ${directory}; update the pinned SDK or the package`);
  }
}

function isFile(path) {
  return statSync(path, { throwIfNoEntry: false })?.isFile() ?? false;
}
