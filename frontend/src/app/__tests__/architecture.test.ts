/// <reference types="node" />
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';

const sourceRoot = fileURLToPath(new URL('../../', import.meta.url));
function files(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const filename = path.join(directory, entry.name);
    return entry.isDirectory() ? files(filename) : [filename];
  });
}
const modules = files(sourceRoot).filter(
  (filename) => /\.(ts|tsx)$/.test(filename) && !filename.includes('.test.'),
);
const relative = (filename: string) => path.relative(sourceRoot, filename).replaceAll('\\', '/');
function imports(filename: string) {
  return ts
    .preProcessFile(readFileSync(filename, 'utf8'), true, true)
    .importedFiles.map((entry) => entry.fileName)
    .filter((specifier) => specifier.startsWith('.'))
    .map((specifier) => {
      const target = path.resolve(path.dirname(filename), specifier);
      const resolved = [
        target,
        target + '.ts',
        target + '.tsx',
        path.join(target, 'index.ts'),
      ].find((candidate) => existsSync(candidate) && statSync(candidate).isFile());
      if (!resolved)
        throw new Error('Unresolved import: ' + relative(filename) + ' → ' + specifier);
      return resolved;
    });
}
const graph = new Map(modules.map((filename) => [filename, imports(filename)]));

describe('feature architecture', () => {
  it('keeps shared independent and prevents upward or cross-feature imports', () => {
    const violations: string[] = [];
    for (const [filename, dependencies] of graph) {
      const from = relative(filename).split('/');
      for (const dependency of dependencies) {
        const to = relative(dependency).split('/');
        if (
          (from[0] === 'shared' && to[0] !== 'shared') ||
          (from[0] === 'features' && !['features', 'shared'].includes(to[0] ?? '')) ||
          (from[0] === 'features' && to[0] === 'features' && from[1] !== to[1]) ||
          (from[0] === 'pages' && !['pages', 'features', 'shared'].includes(to[0] ?? ''))
        )
          violations.push(relative(filename) + ' → ' + relative(dependency));
      }
    }
    expect(violations).toEqual([]);
  });

  it('uses each feature public TypeScript API from other layers', () => {
    const violations: string[] = [];
    for (const [filename, dependencies] of graph) {
      if (relative(filename).startsWith('features/')) continue;
      for (const dependency of dependencies) {
        const target = relative(dependency);
        if (
          /\.tsx?$/.test(target) &&
          target.startsWith('features/') &&
          !/^features\/[^/]+\/index\.ts$/.test(target)
        ) {
          violations.push(relative(filename) + ' → ' + target);
        }
      }
    }
    expect(violations).toEqual([]);
  });

  it('has no circular runtime or type imports', () => {
    const visited = new Set<string>();
    const stack: string[] = [];
    function visit(filename: string) {
      if (stack.includes(filename))
        throw new Error(stack.concat(filename).map(relative).join(' → '));
      if (visited.has(filename)) return;
      stack.push(filename);
      for (const dependency of graph.get(filename) ?? []) visit(dependency);
      stack.pop();
      visited.add(filename);
    }
    for (const filename of modules) visit(filename);
  });

  it('preserves all application routes', () => {
    const filename = path.join(sourceRoot, 'app/router.tsx');
    const ast = ts.createSourceFile(
      filename,
      readFileSync(filename, 'utf8'),
      ts.ScriptTarget.Latest,
      true,
    );
    const routes: string[] = [];
    function visit(node: ts.Node) {
      if (
        ts.isPropertyAssignment(node) &&
        node.name.getText(ast) === 'path' &&
        ts.isStringLiteral(node.initializer)
      )
        routes.push(node.initializer.text);
      ts.forEachChild(node, visit);
    }
    visit(ast);
    expect(routes).toEqual([
      '/login',
      '/',
      '/chat',
      '/documents',
      '/users',
      '/templates',
      '/templates/drafts/:id',
      '/templates/results/:id',
      '/templates/register',
      '*',
    ]);
  });
});
