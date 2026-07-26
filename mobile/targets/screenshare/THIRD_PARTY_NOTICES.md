# Third-party notices

## Jitsi Meet SDK screen-sharing sample

`Atomic.swift`, `DarwinNotificationCenter.swift`, `SampleHandler.swift`,
`SampleUploader.swift`, and `SocketConnection.swift` are vendored from the
Jitsi Meet SDK samples repository:

- Upstream: <https://github.com/jitsi/jitsi-meet-sdk-samples>
- Commit: `bc8b8f06da0aa94f47c7b16da901902975427daf`
- Upstream path: `ios/swift-screensharing/JitsiSDKScreenSharingTest/Broadcast Extension/`
- License: Apache License 2.0; see `LICENSE-JITSI-APACHE-2.0.txt` in this directory.

BonfireOS modification: `SampleHandler.swift` changes the sample App Group
identifier from `group.com.jitsi.example-screensharing.appgroup` to
`group.xyz.thebonfire.app`. The modified file carries a prominent notice next
to its original header. The other four Swift files are copied byte-for-byte
from the identified upstream commit.
