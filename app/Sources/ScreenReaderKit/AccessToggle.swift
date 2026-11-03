import Foundation

/// The menu bar's agent-access toggle: what the config's `access` mode means,
/// what the next mode is, and how the menu item reads.
///
/// The mode itself belongs to the Go server (config key `access`, enforced per
/// call in `server/internal/tools/access.go`); this only spells it for the
/// menu, so it must not invent its own rules about which values are full.
public enum AccessToggle {
    /// Config value for the unrestricted tool surface (also the default when
    /// the key is absent). Mirrors `tools.AccessFull`.
    public static let full = "full"
    /// Config value for the allowlisted surface. Mirrors `tools.AccessReadOnly`.
    public static let readOnly = "read_only"

    /// Whether the server enforces the read-only allowlist for this value.
    /// Mirrors `tools.Allow`: only `full` (and the empty value) are full, so an
    /// unrecognized value reports read-only — which is exactly what the agent
    /// gets, fail closed.
    public static func isReadOnly(_ access: String) -> Bool {
        access != full && access != ""
    }

    /// The mode to write when the item is clicked. Toggling away from an
    /// unrecognized value yields `full`, which repairs the config.
    public static func next(_ access: String) -> String {
        isReadOnly(access) ? full : readOnly
    }

    /// Menu title: the CURRENT mode plus what clicking does. A bare
    /// "Access: read-only" would read as a status label rather than a button,
    /// and after the click it would look like nothing happened.
    /// The config value `read_only` is displayed "read-only".
    public static func title(_ access: String) -> String {
        isReadOnly(access)
            ? "Access: read-only (switch to full)"
            : "Access: full (switch to read-only)"
    }
}
