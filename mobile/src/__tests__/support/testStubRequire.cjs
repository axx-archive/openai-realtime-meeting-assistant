const Module = require('node:module');
const { transformSync } = require('esbuild');

const registries = [];
const originalLoad = Module._load;
let patched = false;

function compileStub(registry, url, parent) {
  if (registry.cache.has(url)) return registry.cache.get(url).exports;
  const instance = new Module(url, parent);
  instance.filename = url;
  instance.paths = parent?.paths ?? [];
  registry.cache.set(url, instance);
  try {
    const code = transformSync(registry.modules[url], {
      format: 'cjs',
      loader: 'js',
      platform: 'node',
      target: 'node20',
    }).code;
    instance._compile(code, url);
    instance.loaded = true;
    return instance.exports;
  } catch (error) {
    registry.cache.delete(url);
    throw error;
  }
}

module.exports = function registerTestStubModules(prefix, modules) {
  registries.unshift({ prefix, modules, cache: new Map() });
  if (patched) return;
  patched = true;
  Module._load = function testStubLoad(request, parent, isMain) {
    for (const registry of registries) {
      const url = `${registry.prefix}${request}`;
      if (Object.prototype.hasOwnProperty.call(registry.modules, url)) {
        return compileStub(registry, url, parent);
      }
    }
    return originalLoad.call(this, request, parent, isMain);
  };
};
