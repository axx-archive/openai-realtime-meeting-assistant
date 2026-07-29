/** @type {import('@bacons/apple-targets/app.plugin').Config} */
module.exports = {
  type: 'broadcast-upload',
  name: 'BonfireOSBroadcastExtension',
  displayName: 'Stride Screen Share',
  bundleIdentifier: 'xyz.thebonfire.app.broadcast',
  deploymentTarget: '16.4',
  entitlements: {
    'com.apple.security.application-groups': ['group.xyz.thebonfire.app'],
  },
};
