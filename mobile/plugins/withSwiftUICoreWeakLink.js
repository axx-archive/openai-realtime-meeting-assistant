const { createRunOncePlugin, withDangerousMod } = require('@expo/config-plugins');
const fs = require('node:fs');
const path = require('node:path');

const pluginName = 'with-swiftui-core-link';
const pluginVersion = '2.0.0';

const marker = '# BonfireOS: link SwiftUI so the iOS 26 glass surfaces resolve SwiftUICore.';

/**
 * Xcode 26 refuses an implicit link against SwiftUICore:
 *
 *   Could not parse or use implicit file '.../SwiftUICore.framework/SwiftUICore.tbd':
 *   cannot link directly with 'SwiftUICore' because product being built is not an
 *   allowed client of it
 *
 * SwiftUICore is a private implementation framework. Building the app against the
 * iOS 26 SDK leaves an implicit reference to it in the main target, and the linker
 * rejects a direct link.
 *
 * The fix is to link the PUBLIC SwiftUI umbrella, which *is* an allowed client and
 * re-exports SwiftUICore — not to weak-link SwiftUICore itself. `-weak_framework
 * SwiftUICore` is still a direct link and fails with the same diagnostic (learned
 * the hard way: v1.0.0 of this plugin did exactly that and moved the failure onto
 * the broadcast extension).
 *
 * Scope matters: ONLY the app target. The ReplayKit broadcast extension has no
 * SwiftUI in it, and adding the flag there is what broke it.
 *
 * This lives in a config plugin rather than in ios/Podfile because ios/ is
 * gitignored and EAS re-runs prebuild from scratch on every cloud build — a hand
 * edit to the Podfile would work locally and then fail in CI.
 */
const APP_TARGET = 'BonfireOS';

const withSwiftUICoreLink = (config) =>
  withDangerousMod(config, [
    'ios',
    async (modConfig) => {
      const podfilePath = path.join(modConfig.modRequest.platformProjectRoot, 'Podfile');
      const podfile = fs.readFileSync(podfilePath, 'utf8');

      if (podfile.includes(marker)) {
        return modConfig;
      }

      const anchor = 'react_native_post_install(';
      const anchorIndex = podfile.indexOf(anchor);
      if (anchorIndex === -1) {
        throw new Error(
          `${pluginName}: could not find react_native_post_install in the Podfile — the ` +
            'Expo template changed and this plugin needs a new anchor.',
        );
      }
      const closeIndex = podfile.indexOf('    )', anchorIndex);
      if (closeIndex === -1) {
        throw new Error(`${pluginName}: could not find the end of react_native_post_install.`);
      }
      const insertAt = closeIndex + '    )'.length;

      const block = [
        '',
        '',
        `    ${marker}`,
        '    installer.aggregate_targets.each do |aggregate_target|',
        '      aggregate_target.user_project.native_targets.each do |native_target|',
        `        next unless native_target.name == '${APP_TARGET}'`,
        '        native_target.build_configurations.each do |build_configuration|',
        "          flags = build_configuration.build_settings['OTHER_LDFLAGS'] || ['$(inherited)']",
        '          flags = [flags] if flags.is_a?(String)',
        "          next if flags.join(' ').include?('-framework SwiftUI')",
        "          build_configuration.build_settings['OTHER_LDFLAGS'] = flags + ['-framework', 'SwiftUI']",
        '        end',
        '      end',
        '      aggregate_target.user_project.save',
        '    end',
      ].join('\n');

      fs.writeFileSync(podfilePath, podfile.slice(0, insertAt) + block + podfile.slice(insertAt));
      return modConfig;
    },
  ]);

module.exports = createRunOncePlugin(withSwiftUICoreLink, pluginName, pluginVersion);
