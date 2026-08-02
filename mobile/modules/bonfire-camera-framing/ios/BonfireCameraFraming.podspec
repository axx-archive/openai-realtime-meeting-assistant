require 'json'

package = JSON.parse(File.read(File.join(__dir__, '..', 'package.json')))

Pod::Spec.new do |s|
  s.name           = 'BonfireCameraFraming'
  s.version        = package['version']
  s.summary        = package['description']
  s.description    = package['description']
  s.license        = { type: 'UNLICENSED' }
  s.author         = 'Axxon Labs'
  s.homepage       = 'https://thebonfire.xyz'
  s.platforms      = { ios: '16.4' }
  s.source         = { path: '.' }
  s.static_framework = true
  s.swift_version  = '5.9'

  s.dependency 'ExpoModulesCore'
  # The crash boundary below is reviewed against M124's exact Objective-C
  # selector and output-settings behavior. Fail dependency resolution instead
  # of silently shipping it against an unreviewed WebRTC binary.
  s.dependency 'JitsiWebRTC', '= 124.0.2'

  s.frameworks = 'AVFoundation', 'UIKit'
  s.source_files = '**/*.{h,m,mm,swift}'
  s.public_header_files = '**/*.h'
  s.pod_target_xcconfig = {
    'DEFINES_MODULE' => 'YES',
    'SWIFT_COMPILATION_MODE' => 'wholemodule'
  }
end
