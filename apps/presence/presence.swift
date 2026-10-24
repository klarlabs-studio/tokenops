// tokenops-presence holds this Mac's presence key for TokenOps (ADR 0015):
// a P-256 key in the Secure Enclave, created with .userPresence, so every
// signature needs Touch ID, a paired Apple Watch or the login password.
//
// The CLI and the daemon are pure Go and only exec this helper; they build
// every statement it signs and verify every signature it returns against
// the enrolled public key, so a replaced helper gains nothing. The key is an
// opaque blob only this Mac's Secure Enclave can use; it is not bound to
// this binary's signature, so an ad hoc re-sign on upgrade keeps it.
//
//   tokenops-presence enroll --blob <path>
//       create a key, write its blob to <path> (0600), print its public key
//       (raw X9.63, base64). No dialog.
//   tokenops-presence sign --blob <path> --reason <text> [--message-file <path>]
//       sign the message (stdin, or the file) after the system dialog, whose
//       text is <text>; print the ASN.1 DER ECDSA signature over SHA-256 of
//       the message, base64.
//   tokenops-presence pubkey --blob <path>
//       print the blob's public key. No dialog.
//   tokenops-presence check
//       whether a signature could be asked for here: a Secure Enclave, the
//       logged-in user's screen, unlocked. No dialog.
//
// Exit codes: 0 ok, 1 other failure, 2 usage, 3 no Secure Enclave, 4 nobody
// can be asked here (SSH, a launchd job, CI), 5 the screen is locked,
// 6 cancelled, 7 authentication failed, 8 the blob is not a key this Secure
// Enclave holds. It fails fast rather than waiting for a dialog that cannot
// be shown.
import CoreGraphics
import CryptoKit
import Foundation
import LocalAuthentication
import Security

enum Exit: Int32 {
    case ok = 0, failure = 1, usage = 2, noEnclave = 3, noGUI = 4, locked = 5
    case cancelled = 6, authFailed = 7, badKey = 8
}

let version = "1"

func fail(_ msg: String, _ code: Exit) -> Never {
    FileHandle.standardError.write(("tokenops-presence: " + msg + "\n").data(using: .utf8)!)
    exit(code.rawValue)
}

let args = Array(CommandLine.arguments.dropFirst())

func flag(_ name: String) -> String? {
    guard let i = args.firstIndex(of: "--" + name), i + 1 < args.count else { return nil }
    return args[i + 1]
}

func required(_ name: String) -> String {
    guard let v = flag(name), !v.isEmpty else { fail("--\(name) is required", .usage) }
    return v
}

func requireEnclave() {
    guard SecureEnclave.isAvailable else { fail("this Mac has no Secure Enclave", .noEnclave) }
}

// requireScreen fails fast when no dialog can be shown: no graphical
// session (SSH, a launchd daemon, CI), a session not on the console (fast
// user switching), or a locked screen. LocalAuthentication would otherwise
// refuse later, or wait.
func requireScreen() {
    var session = SecuritySessionId()
    var attrs = SessionAttributeBits()
    let status = SessionGetInfo(SecuritySessionId(bitPattern: -1), &session, &attrs)
    guard status == errSecSuccess, attrs.contains(.sessionHasGraphicAccess) else {
        fail("no one can be asked here: the dialog needs the logged-in user's screen (not SSH, a launchd job or CI)", .noGUI)
    }
    guard let info = CGSessionCopyCurrentDictionary() as? [String: Any] else {
        fail("no one can be asked here: there is no window server session", .noGUI)
    }
    if let onConsole = info[kCGSessionOnConsoleKey as String] as? Bool, !onConsole {
        fail("no one can be asked here: this user's session is not on the screen", .noGUI)
    }
    if let locked = info["CGSSessionScreenIsLocked"] as? Bool, locked {
        fail("the screen is locked, so the dialog cannot be shown", .locked)
    }
}

// exitCode names why LocalAuthentication or the Secure Enclave refused,
// walking underlying errors.
func exitCode(for error: Error) -> Exit {
    var next: NSError? = error as NSError
    while let e = next {
        if e.domain == LAErrorDomain {
            switch e.code {
            case LAError.userCancel.rawValue, LAError.appCancel.rawValue, LAError.userFallback.rawValue:
                return .cancelled
            case LAError.systemCancel.rawValue:
                // Seen when the console is locked or another system
                // authentication is running: nothing was shown.
                return .locked
            case LAError.notInteractive.rawValue:
                return .noGUI
            case LAError.authenticationFailed.rawValue, LAError.biometryLockout.rawValue:
                return .authFailed
            default:
                return .failure
            }
        }
        next = e.userInfo[NSUnderlyingErrorKey] as? NSError
    }
    return .failure
}

// writeBlob writes the key's blob owner-only, atomically.
func writeBlob(_ data: Data, to path: String) throws {
    let url = URL(fileURLWithPath: path)
    let tmp = url.deletingLastPathComponent().appendingPathComponent(".\(url.lastPathComponent).\(getpid())")
    guard FileManager.default.createFile(atPath: tmp.path, contents: data, attributes: [.posixPermissions: 0o600]) else {
        throw CocoaError(.fileWriteUnknown)
    }
    if rename(tmp.path, url.path) != 0 {
        unlink(tmp.path)
        throw CocoaError(.fileWriteUnknown)
    }
}

func loadKey(_ path: String, context: LAContext? = nil) -> SecureEnclave.P256.Signing.PrivateKey {
    let blob: Data
    do {
        blob = try Data(contentsOf: URL(fileURLWithPath: path))
    } catch {
        fail("no key blob at \(path)", .badKey)
    }
    do {
        if let context {
            return try SecureEnclave.P256.Signing.PrivateKey(dataRepresentation: blob, authenticationContext: context)
        }
        return try SecureEnclave.P256.Signing.PrivateKey(dataRepresentation: blob)
    } catch {
        fail("the key blob is not a key this Mac's Secure Enclave holds: \(error)", .badKey)
    }
}

func enroll() {
    requireEnclave()
    let path = required("blob")
    var err: Unmanaged<CFError>?
    // AfterFirstUnlock: WhenUnlocked fails with -25308 outside a GUI
    // session (ADR 0015 spike), and every signature needs presence anyway.
    guard let access = SecAccessControlCreateWithFlags(
        nil, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly, [.privateKeyUsage, .userPresence], &err
    ) else {
        fail("access control: \(String(describing: err?.takeRetainedValue()))", .failure)
    }
    do {
        let key = try SecureEnclave.P256.Signing.PrivateKey(accessControl: access)
        try writeBlob(key.dataRepresentation, to: path)
        print(key.publicKey.x963Representation.base64EncodedString())
    } catch {
        fail("could not create the key: \(error)", .failure)
    }
}

func sign() {
    requireEnclave()
    let path = required("blob")
    let reason = required("reason")
    let message: Data
    if let file = flag("message-file") {
        guard let m = FileManager.default.contents(atPath: file) else { fail("cannot read \(file)", .usage) }
        message = m
    } else {
        message = FileHandle.standardInput.readDataToEndOfFile()
    }
    requireScreen()
    let context = LAContext()
    context.localizedReason = reason
    // Never reuse an earlier Touch ID match: one dialog per approval.
    context.touchIDAuthenticationAllowableReuseDuration = 0
    let key = loadKey(path, context: context)
    do {
        let sig = try key.signature(for: message)
        print(sig.derRepresentation.base64EncodedString())
    } catch {
        let code = exitCode(for: error)
        fail("no signature: \(error.localizedDescription)", code)
    }
}

func pubkey() {
    requireEnclave()
    print(loadKey(required("blob")).publicKey.x963Representation.base64EncodedString())
}

func check() {
    requireEnclave()
    requireScreen()
    var err: NSError?
    guard LAContext().canEvaluatePolicy(.deviceOwnerAuthentication, error: &err) else {
        fail("this user cannot authenticate here: \(err?.localizedDescription ?? "unknown")", .noGUI)
    }
    print("ok")
}

switch args.first {
case "enroll": enroll()
case "sign": sign()
case "pubkey": pubkey()
case "check": check()
case "version", "--version": print("tokenops-presence \(version)")
default:
    fail("usage: tokenops-presence enroll|sign|pubkey|check|version (see the source header)", .usage)
}
