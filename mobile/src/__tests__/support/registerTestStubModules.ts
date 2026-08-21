import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const installTestStubModules = require('./testStubRequire.cjs') as (
  prefix: string,
  modules: Record<string, string>,
) => void;

/**
 * Register source-only stubs through the CommonJS loader used by tsx for this
 * package. This keeps the harness on Node 20 without relying on the newer
 * synchronous `module.registerHooks` API.
 */
export function registerTestStubModules(prefix: string, modules: Record<string, string>): void {
  installTestStubModules(prefix, modules);
}
