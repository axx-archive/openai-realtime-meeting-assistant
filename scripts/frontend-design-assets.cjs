// Real shared design resources for the existing synthetic HTTP fixtures.
// Leave each fixture's API behavior and other asset overrides untouched.
const fs = require('node:fs');
const path = require('node:path');
const assets = new Map([
  ['/public/stride-operating.css', 'text/css'],
  ['/public/stride-wordmark-black.png', 'image/png'],
  ...['google-sans-flex-400.ttf','google-sans-flex-500.ttf','google-sans-flex-600.ttf','google-sans-flex-700.ttf','geist-mono-400.ttf','geist-mono-500.ttf'].map(name => ['/public/fonts/' + name, 'font/ttf']),
  ['/public/design/stride-tokens.css', 'text/css'],
  ['/public/design/legacy-tokens.css', 'text/css'],
  ['/public/design/appearance.js', 'application/javascript'],
]);
module.exports = function serveDesignAsset(req, res) {
  const name = new URL(req.url, 'http://fixture.test').pathname;
  const mime = assets.get(name);
  if (!mime) return false;
  const content = fs.readFileSync(path.join(__dirname, '..', name));
  res.writeHead(200, { 'content-type': mime, 'cache-control': 'no-store' });
  res.end(content);
  return true;
};
