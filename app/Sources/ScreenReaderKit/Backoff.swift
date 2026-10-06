import Foundation

public enum Backoff {
    /// Restart delay (seconds) for the given 0-based crash attempt: 1, 2, 4, … capped at 30.
    public static func delay(attempt: Int) -> Double {
        min(pow(2.0, Double(max(0, attempt))), 30.0)
    }
}
