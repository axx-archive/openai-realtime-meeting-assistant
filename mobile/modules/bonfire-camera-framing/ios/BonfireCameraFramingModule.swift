import ExpoModulesCore

public final class BonfireCameraFramingModule: Module {
  public func definition() -> ModuleDefinition {
    Name("BonfireCameraFraming")

    AsyncFunction("getCapabilities") { (deviceID: String) in
      return BonfireCameraDeviceGuard.capabilities(deviceID: deviceID)
    }

    AsyncFunction("setCenterStageEnabled") {
      (enabled: Bool, deviceID: String, promise: Promise) in
      BonfireCameraDeviceGuard.setCenterStage(
        enabled: enabled,
        deviceID: deviceID
      ) { result in
        promise.resolve(result)
      }
    }

    AsyncFunction("setWideUprightFramingEnabled") {
      (enabled: Bool, deviceID: String, promise: Promise) in
      BonfireCameraDeviceGuard.setWideUprightFraming(
        enabled: enabled,
        deviceID: deviceID
      ) { result in
        promise.resolve(result)
      }
    }
  }
}
