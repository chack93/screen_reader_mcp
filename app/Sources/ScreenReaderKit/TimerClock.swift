import Foundation

/// Formats an interval as "H:MM" (e.g. "3:49", "0:05") for the menu's
/// "Server: stop (running for …)" line — the 4h timer's current value.
public enum TimerClock {
    public static func format(_ interval: TimeInterval) -> String {
        let s = max(0, Int(interval))
        return "\(s / 3600):" + String(format: "%02d", s / 60 % 60)
    }
}
