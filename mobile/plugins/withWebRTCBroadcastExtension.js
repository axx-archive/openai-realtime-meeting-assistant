const {
  withXcodeProjectBeta,
} = require('@bacons/apple-targets/build/with-bacons-xcode');

const pluginName = 'with-webrtc-broadcast-extension';
const mainBundleIdentifier = 'xyz.thebonfire.app';
const extensionBundleIdentifier = 'xyz.thebonfire.app.broadcast';
const extensionTargetName = 'BonfireOSBroadcastExtension';
const appGroupIdentifier = 'group.xyz.thebonfire.app';

function getBuildConfigurations(target) {
  return target.props.buildConfigurationList.props.buildConfigurations;
}

function withWebRTCBroadcastExtension(config, { stage = 'native' } = {}) {
  if (stage !== 'native' && stage !== 'eas') {
    throw new Error(`${pluginName} received unsupported stage: ${stage}`);
  }

  const appExtensions =
    config.extra?.eas?.build?.experimental?.ios?.appExtensions;
  const appExtension = appExtensions?.find(
    (candidate) => candidate.bundleIdentifier === extensionBundleIdentifier,
  );

  if (!appExtension) {
    throw new Error(
      `${pluginName} expected @bacons/apple-targets to declare ${extensionBundleIdentifier} for EAS signing.`,
    );
  }

  // @bacons/apple-targets derives the signing declaration while evaluating
  // the config. Keep the parent explicit so EAS can resolve the nested target
  // before the Xcode project exists.
  appExtension.targetName = extensionTargetName;
  appExtension.parentBundleIdentifier = mainBundleIdentifier;
  appExtension.entitlements = {
    'com.apple.security.application-groups': [appGroupIdentifier],
  };

  // The target generator's custom Xcode provider must be registered last.
  // Run this same plugin once before it to queue native checks and once after
  // it to restore EAS-only metadata that the generator normalizes.
  if (stage === 'eas') {
    return config;
  }

  const releaseBuildNumber = config.ios?.buildNumber;
  const releaseVersion = config.version;
  if (!releaseBuildNumber || !releaseVersion) {
    throw new Error(
      `${pluginName} requires both expo.version and expo.ios.buildNumber so the app and extension ship with identical versions.`,
    );
  }

  return withXcodeProjectBeta(config, async (xcodeConfig) => {
    const nativeTargets = xcodeConfig.modResults.rootObject.props.targets;
    const extensionTarget = nativeTargets.find(
      (target) => target.props.productName === extensionTargetName,
    );
    const mainTarget = nativeTargets.find(
      (target) =>
        target.props.productType === 'com.apple.product-type.application' &&
        target.props.productName !== extensionTargetName,
    );

    if (!extensionTarget || !mainTarget) {
      throw new Error(
        `${pluginName} could not find both ${extensionTargetName} and the main application target.`,
      );
    }

    const mainConfigurations = new Map(
      getBuildConfigurations(mainTarget).map((buildConfiguration) => [
        buildConfiguration.props.name,
        buildConfiguration,
      ]),
    );

    for (const extensionConfiguration of getBuildConfigurations(
      extensionTarget,
    )) {
      const mainConfiguration = mainConfigurations.get(
        extensionConfiguration.props.name,
      );
      if (!mainConfiguration) {
        throw new Error(
          `${pluginName} could not match the ${extensionConfiguration.props.name} build configuration.`,
        );
      }

      const mainSettings = mainConfiguration.props.buildSettings;
      const extensionSettings = extensionConfiguration.props.buildSettings;

      extensionSettings.APPLICATION_EXTENSION_API_ONLY = 'YES';
      // Expo's build-time configure phase writes the public config version to
      // the containing app's Info.plist. The generated Xcode project still
      // starts at build 1, so copying its settings would leave the extension
      // mismatched and fail App Store validation. Pin both targets directly to
      // the same local release values before Xcode compiles either bundle.
      mainSettings.CURRENT_PROJECT_VERSION = releaseBuildNumber;
      mainSettings.MARKETING_VERSION = releaseVersion;
      extensionSettings.CURRENT_PROJECT_VERSION = releaseBuildNumber;
      extensionSettings.MARKETING_VERSION = releaseVersion;
    }

    return xcodeConfig;
  });
}

module.exports = withWebRTCBroadcastExtension;
