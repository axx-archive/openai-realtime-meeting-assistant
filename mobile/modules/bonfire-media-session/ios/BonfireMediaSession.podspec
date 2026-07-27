require 'json'

package = JSON.parse(File.read(File.join(__dir__, '..', 'package.json')))

Pod::Spec.new do |s|
  s.name           = 'BonfireMediaSession'
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
  s.dependency 'JitsiWebRTC', '~> 124.0.0'

  s.frameworks = 'AVFoundation'
  s.source_files = '**/*.{h,m,mm,swift}'
  s.pod_target_xcconfig = {
    'DEFINES_MODULE' => 'YES',
    'SWIFT_COMPILATION_MODE' => 'wholemodule'
  }
end
