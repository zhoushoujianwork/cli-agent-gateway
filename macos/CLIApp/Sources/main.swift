import AppKit
import CryptoKit
import Foundation
import SwiftUI

enum ChannelType: String, CaseIterable, Identifiable {
    case imessage
    case dingtalk

    var id: String { rawValue }

    var title: String {
        switch self {
        case .imessage:
            return "iMessage"
        case .dingtalk:
            return "DingTalk"
        }
    }
}

enum RepairAction: String {
    case setupEnv
    case installCodexACP
    case installIMsg
}

struct HealthCheckItem: Identifiable {
    let id: String
    let title: String
    let ok: Bool
    let detail: String
    let repairAction: RepairAction?
}

struct GatewayConfig: Decodable {
    let repoRoot: String
    let workdir: String
    let lockFile: String
    let logFile: String
    let stateFile: String
    let interactionLogFile: String
}

struct SessionEntry: Identifiable {
    let sessionKey: String
    let sessionId: String
    let channel: String
    let senderId: String
    let sender: String
    let threadId: String
    let lastText: String
    let lastTime: String
    let latest: Bool

    var id: String { sessionKey }
}

enum MessageDeliveryStatus: String {
    case sending
    case sent
    case failed
    case action
}

struct ChatMessage: Identifiable {
    let id: String
    let sourceMsgId: String
    let role: String
    let text: String
    let time: String
    let deliveryStatus: MessageDeliveryStatus?
    let statusDetail: String
}

struct ProcessEvent: Identifiable {
    let id: String
    let time: String
    let title: String
    let detail: String
}

enum GatewayError: Error, LocalizedError {
    case missingConfig
    case invalidConfig(String)

    var errorDescription: String? {
        switch self {
        case .missingConfig:
            return "Missing bundled gateway_config.json"
        case .invalidConfig(let msg):
            return msg
        }
    }
}

final class GUILogger {
    static let shared = GUILogger()

    private let queue = DispatchQueue(label: "cag.gui.logger", qos: .utility)
    private let fmt = ISO8601DateFormatter()
    private var logPath: String

    private init() {
        let base = ("~/Library/Logs/cli-agent-gateway" as NSString).expandingTildeInPath
        try? FileManager.default.createDirectory(atPath: base, withIntermediateDirectories: true)
        logPath = URL(fileURLWithPath: base).appendingPathComponent("gui.log").path
    }

    func setLogPath(_ path: String) {
        queue.async { [weak self] in
            guard let self else { return }
            let dir = URL(fileURLWithPath: path).deletingLastPathComponent().path
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            self.logPath = path
            self.writeLine("logger path set path=\(path)")
        }
    }

    func log(_ message: String) {
        queue.async { [weak self] in
            self?.writeLine(message)
        }
    }

    private func writeLine(_ message: String) {
        let ts = fmt.string(from: Date())
        let line = "[\(ts)] \(message)\n"
        guard let data = line.data(using: .utf8) else { return }
        if FileManager.default.fileExists(atPath: logPath) {
            if let fh = try? FileHandle(forWritingTo: URL(fileURLWithPath: logPath)) {
                defer { try? fh.close() }
                _ = try? fh.seekToEnd()
                try? fh.write(contentsOf: data)
                return
            }
        }
        try? data.write(to: URL(fileURLWithPath: logPath), options: .atomic)
    }
}

final class GatewayController: ObservableObject {
    @Published var statusText: String = "Checking status..."
    @Published var activeChannelText: String = "Unknown"
    @Published var detailText: String = ""
    @Published var selectedChannel: ChannelType
    @Published var sessions: [SessionEntry] = []
    @Published var selectedSessionKey: String?
    @Published var chatMessages: [ChatMessage] = []
    @Published var healthChecks: [HealthCheckItem] = []
    @Published var timelineByMsgId: [String: [ProcessEvent]] = [:]
    @Published var localDraftText: String = ""
    @Published var localSending: Bool = false
    @Published var currentLogFile: String = ""

    private let cfg: GatewayConfig
    private let channelDefaultsKey = "gateway.selected_channel"
    private let hiddenSessionsDefaultsPrefix = "gateway.hidden_sessions"
    private var hiddenSessionCutoffByKey: [String: String] = [:]
    private var localOverlayMessagesBySession: [String: [ChatMessage]] = [:]
    private var lastLocalSendFingerprint: String = ""
    private var lastLocalSendAt: Date = .distantPast
    private var didAutoStartOnLaunch = false
    private let refreshLock = NSLock()
    private var refreshingHealth = false
    private var refreshingStatus = false
    private var refreshingSessions = false

    init() throws {
        cfg = try GatewayController.loadConfig()
        selectedChannel = GatewayController.detectEnvChannel(repoRoot: cfg.repoRoot)
        hiddenSessionCutoffByKey = loadHiddenSessionCutoffByKey()
        currentLogFile = cfg.logFile
        let guiLogPath = URL(fileURLWithPath: cfg.logFile).deletingLastPathComponent().appendingPathComponent("gui.log").path
        GUILogger.shared.setLogPath(guiLogPath)
        log("controller init repo=\(cfg.repoRoot) workdir=\(cfg.workdir)")
    }

    private var hiddenSessionsDefaultsKey: String {
        "\(hiddenSessionsDefaultsPrefix).\(cfg.repoRoot)"
    }

    private func nowISO8601() -> String {
        ISO8601DateFormatter().string(from: Date())
    }

    private func log(_ message: String) {
        GUILogger.shared.log(message)
    }

    private func loadHiddenSessionCutoffByKey() -> [String: String] {
        guard let raw = UserDefaults.standard.dictionary(forKey: hiddenSessionsDefaultsKey) else {
            return [:]
        }
        var out: [String: String] = [:]
        for (k, v) in raw {
            guard let ts = v as? String else { continue }
            out[k] = ts
        }
        return out
    }

    private func saveHiddenSessionCutoffByKey() {
        UserDefaults.standard.set(hiddenSessionCutoffByKey, forKey: hiddenSessionsDefaultsKey)
    }

    private func hideSessionKey(_ key: String) {
        hiddenSessionCutoffByKey[key] = nowISO8601()
    }

    private func shouldShowSession(_ session: SessionEntry) -> Bool {
        guard let cutoff = hiddenSessionCutoffByKey[session.sessionKey] else {
            return true
        }
        if session.lastTime > cutoff {
            hiddenSessionCutoffByKey.removeValue(forKey: session.sessionKey)
            return true
        }
        return false
    }

    private static func loadConfig() throws -> GatewayConfig {
        guard let url = Bundle.main.url(forResource: "gateway_config", withExtension: "json") else {
            throw GatewayError.missingConfig
        }
        let data = try Data(contentsOf: url)
        let decoded = try JSONDecoder().decode(GatewayConfig.self, from: data)
        if decoded.repoRoot.isEmpty || decoded.workdir.isEmpty {
            throw GatewayError.invalidConfig("Invalid repoRoot/workdir in app config.")
        }
        return decoded
    }

    private static func detectEnvChannel(repoRoot: String) -> ChannelType {
        let envPath = URL(fileURLWithPath: repoRoot).appendingPathComponent(".env").path
        guard let text = try? String(contentsOfFile: envPath, encoding: .utf8) else {
            return .dingtalk
        }
        for line in text.split(separator: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("CHANNEL_TYPE=") {
                let value = String(trimmed.dropFirst("CHANNEL_TYPE=".count)).replacingOccurrences(of: "\"", with: "")
                return ChannelType(rawValue: value) ?? .dingtalk
            }
        }
        return .dingtalk
    }

    private static func loadSavedChannel(defaultChannel: ChannelType) -> ChannelType {
        guard let raw = UserDefaults.standard.string(forKey: "gateway.selected_channel") else {
            return defaultChannel
        }
        return ChannelType(rawValue: raw) ?? defaultChannel
    }

    private var envPath: String { URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent(".env").path }

    private func envValue(_ key: String) -> String? {
        guard let text = try? String(contentsOfFile: envPath, encoding: .utf8) else { return nil }
        for raw in text.split(separator: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.isEmpty || line.hasPrefix("#") { continue }
            guard let idx = line.firstIndex(of: "=") else { continue }
            let k = String(line[..<idx]).trimmingCharacters(in: .whitespaces)
            if k != key { continue }
            let v = String(line[line.index(after: idx)...]).trimmingCharacters(in: .whitespaces)
            return v.trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
        }
        return nil
    }

    private func writeEnvValue(_ key: String, value: String) {
        let path = envPath
        var lines: [String] = []
        if let text = try? String(contentsOfFile: path, encoding: .utf8) {
            lines = text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        }
        var replaced = false
        for idx in lines.indices {
            let trimmed = lines[idx].trimmingCharacters(in: .whitespaces)
            if trimmed.isEmpty || trimmed.hasPrefix("#") { continue }
            guard let eq = trimmed.firstIndex(of: "=") else { continue }
            let k = String(trimmed[..<eq]).trimmingCharacters(in: .whitespaces)
            if k == key {
                lines[idx] = "\(key)=\(value)"
                replaced = true
                break
            }
        }
        if !replaced {
            lines.append("\(key)=\(value)")
        }
        let finalText = lines.joined(separator: "\n") + "\n"
        try? finalText.write(toFile: path, atomically: true, encoding: .utf8)
    }

    private func shellOutput(_ command: String, timeoutSec: TimeInterval? = nil) -> (code: Int32, stdout: String, stderr: String, output: String) {
        let t0 = Date()
        let proc = Process()
        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe
        proc.executableURL = URL(fileURLWithPath: "/bin/zsh")
        proc.arguments = ["-lc", command]
        do {
            try proc.run()
            var outData = Data()
            var errData = Data()
            let readGroup = DispatchGroup()
            readGroup.enter()
            DispatchQueue.global(qos: .utility).async {
                outData = outPipe.fileHandleForReading.readDataToEndOfFile()
                readGroup.leave()
            }
            readGroup.enter()
            DispatchQueue.global(qos: .utility).async {
                errData = errPipe.fileHandleForReading.readDataToEndOfFile()
                readGroup.leave()
            }

            var didTimeout = false
            if let timeoutSec {
                let deadline = Date().addingTimeInterval(timeoutSec)
                while proc.isRunning && Date() < deadline {
                    Thread.sleep(forTimeInterval: 0.05)
                }
                if proc.isRunning {
                    didTimeout = true
                    proc.terminate()
                }
            }
            proc.waitUntilExit()
            readGroup.wait()
            let stdout = String(data: outData, encoding: .utf8) ?? ""
            let stderr = String(data: errData, encoding: .utf8) ?? ""
            var mergedParts = [stdout.trimmingCharacters(in: .whitespacesAndNewlines), stderr.trimmingCharacters(in: .whitespacesAndNewlines)]
            if didTimeout {
                mergedParts.append("[timeout]")
            }
            let merged = mergedParts
                .filter { !$0.isEmpty }
                .joined(separator: "\n")
            let ms = Int(Date().timeIntervalSince(t0) * 1000)
            if didTimeout {
                log("shell timeout code=124 elapsed_ms=\(ms)")
                return (124, stdout, stderr, merged)
            }
            log("shell done code=\(proc.terminationStatus) elapsed_ms=\(ms)")
            return (proc.terminationStatus, stdout, stderr, merged)
        } catch {
            log("shell run error err=\(error.localizedDescription)")
            return (127, "", error.localizedDescription, error.localizedDescription)
        }
    }

    private func commandExists(_ cmd: String) -> Bool {
        let esc = cmd.replacingOccurrences(of: "'", with: "'\\''")
        return shellOutput("command -v '\(esc)' >/dev/null 2>&1").code == 0
    }

    private func fileModDate(_ path: String) -> Date? {
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: path) else { return nil }
        return attrs[.modificationDate] as? Date
    }

    private func cagRunner() -> (workdir: String, prefix: [String]) {
        let binPath = URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent("bin/cag").path
        if FileManager.default.isExecutableFile(atPath: binPath) {
            let srcMain = URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent("src/cmd/gateway-cli/main.go").path
            if let binMod = fileModDate(binPath), let srcMod = fileModDate(srcMain), binMod >= srcMod {
                return (cfg.repoRoot, [binPath])
            }
            log("runner fallback reason=stale_bin bin=\(binPath)")
        }
        let srcPath = URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent("src").path
        return (srcPath, ["go", "run", "./cmd/gateway-cli"])
    }

    private func cagJSON(_ action: String, args: [String] = [], timeoutSec: TimeInterval? = nil) -> (code: Int32, json: [String: Any]?, raw: String) {
        let t0 = Date()
        let runner = cagRunner()
        let cmdParts = runner.prefix + [action] + args + ["--json"]
        let full = cmdParts.map { shellEscape($0) }.joined(separator: " ")
        let cmd = "cd \(shellEscape(runner.workdir)) && \(full)"
        let out = shellOutput(cmd, timeoutSec: timeoutSec)
        let ms = Int(Date().timeIntervalSince(t0) * 1000)
        let parseSource = out.stdout.isEmpty ? out.output : out.stdout
        guard let line = extractLastJSONLine(parseSource) ?? extractLastJSONLine(out.output),
              let data = line.data(using: .utf8),
              let node = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else {
            log("cag json parse failed action=\(action) code=\(out.code) elapsed_ms=\(ms)")
            return (out.code, nil, out.output)
        }
        log("cag json ok action=\(action) code=\(out.code) elapsed_ms=\(ms)")
        return (out.code, node, out.output)
    }

    private func repairActionForCheck(_ key: String) -> RepairAction? {
        if key == "env" || key == "config" {
            return .setupEnv
        }
        if key == "acp" {
            return .installCodexACP
        }
        if key.hasPrefix("imessage") {
            return .installIMsg
        }
        return nil
    }

    private func hasHealthFailures() -> Bool {
        healthChecks.contains(where: { !$0.ok })
    }

    private func onMain(_ block: @escaping () -> Void) {
        if Thread.isMainThread {
            block()
        } else {
            DispatchQueue.main.async(execute: block)
        }
    }

    private func runInBackground(_ block: @escaping () -> Void) {
        DispatchQueue.global(qos: .userInitiated).async(execute: block)
    }

    private func beginRefresh(kind: String) -> Bool {
        refreshLock.lock()
        defer { refreshLock.unlock() }
        switch kind {
        case "health":
            if refreshingHealth { return false }
            refreshingHealth = true
        case "status":
            if refreshingStatus { return false }
            refreshingStatus = true
        case "sessions":
            if refreshingSessions { return false }
            refreshingSessions = true
        default:
            return true
        }
        return true
    }

    private func endRefresh(kind: String) {
        refreshLock.lock()
        defer { refreshLock.unlock() }
        switch kind {
        case "health":
            refreshingHealth = false
        case "status":
            refreshingStatus = false
        case "sessions":
            refreshingSessions = false
        default:
            break
        }
    }

    func refreshHealthChecksAsync() {
        if !beginRefresh(kind: "health") {
            log("refresh skip kind=health reason=inflight")
            return
        }
        log("refresh start kind=health")
        runInBackground { [weak self] in
            defer {
                self?.endRefresh(kind: "health")
                self?.log("refresh end kind=health")
            }
            self?.refreshHealthChecks()
        }
    }

    func refreshStatusAsync() {
        if !beginRefresh(kind: "status") {
            log("refresh skip kind=status reason=inflight")
            return
        }
        log("refresh start kind=status")
        runInBackground { [weak self] in
            defer {
                self?.endRefresh(kind: "status")
                self?.log("refresh end kind=status")
            }
            self?.refreshStatus()
        }
    }

    func refreshSessionsAsync() {
        if !beginRefresh(kind: "sessions") {
            log("refresh skip kind=sessions reason=inflight")
            return
        }
        log("refresh start kind=sessions")
        runInBackground { [weak self] in
            defer {
                self?.endRefresh(kind: "sessions")
                self?.log("refresh end kind=sessions")
            }
            self?.refreshSessions()
        }
    }

    func timeline(for message: ChatMessage) -> [ProcessEvent] {
        timelineByMsgId[message.sourceMsgId, default: []]
    }

    func refreshHealthChecks() {
        let t0 = Date()
        let doctor = cagJSON("doctor")
        let fallback = cagJSON("health")
        let response = doctor.json ?? fallback.json

        guard let node = response else {
            let fallbackChecks = [
                HealthCheckItem(
                    id: "doctor",
                    title: "Gateway doctor",
                    ok: false,
                    detail: "Failed to parse CLI JSON output.",
                    repairAction: nil
                )
            ]
            onMain { [weak self] in
                self?.healthChecks = fallbackChecks
            }
            let ms = Int(Date().timeIntervalSince(t0) * 1000)
            log("refresh result kind=health status=fallback elapsed_ms=\(ms)")
            return
        }

        var checks: [HealthCheckItem] = []
        if let items = node["items"] as? [[String: Any]] {
            for item in items {
                let key = (item["key"] as? String) ?? "check"
                let ok = (item["ok"] as? Bool) ?? false
                let detail = (item["detail"] as? String) ?? ""
                let suggestion = (item["suggestion"] as? String) ?? ""
                let fullDetail = suggestion.isEmpty ? detail : "\(detail)\nSuggestion: \(suggestion)"
                checks.append(
                    HealthCheckItem(
                        id: key,
                        title: key.replacingOccurrences(of: ".", with: " "),
                        ok: ok,
                        detail: fullDetail,
                        repairAction: ok ? nil : repairActionForCheck(key)
                    )
                )
            }
        }
        if checks.isEmpty {
            checks = [
                HealthCheckItem(
                    id: "doctor",
                    title: "Gateway doctor",
                    ok: false,
                    detail: "No checks returned by CLI.",
                    repairAction: nil
                )
            ]
        }
        onMain { [weak self] in
            self?.healthChecks = checks
        }
        let ms = Int(Date().timeIntervalSince(t0) * 1000)
        log("refresh result kind=health status=ok checks=\(checks.count) elapsed_ms=\(ms)")
    }

    func runRepair(_ action: RepairAction) {
        switch action {
        case .setupEnv:
            let cmd = "cd \(shellEscape(cfg.repoRoot)) && make config"
            let esc = cmd.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
            let script = "tell application \"Terminal\" to do script \"\(esc)\""
            _ = shellOutput("osascript -e \"\(script.replacingOccurrences(of: "\"", with: "\\\""))\"")
            detailText = "Opened setup wizard in Terminal. Complete it, then checks will pass."

        case .installCodexACP:
            NSWorkspace.shared.open(URL(string: "https://github.com/openai/codex")!)
            detailText = "Opened Codex setup page. Install codex-acp command manually, then retry."

        case .installIMsg:
            NSWorkspace.shared.open(URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent("docs/IMESSAGE_SETUP.md"))
            detailText = "Opened iMessage setup guide. Install and configure imsg first."

        }
        refreshHealthChecksAsync()
    }

    private func runningPID() -> Int32? {
        let lockURL = URL(fileURLWithPath: cfg.lockFile)
        guard
            let data = try? Data(contentsOf: lockURL),
            let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            let pid = obj["pid"] as? Int
        else {
            return nil
        }
        let pid32 = Int32(pid)
        if kill(pid32, 0) == 0 || errno == EPERM {
            return pid32
        }
        return gatewayPIDsByWorkdir().first
    }

    private func gatewayPIDsByWorkdir() -> [Int32] {
        let runner = cagRunner()
        let statusCmd = (runner.prefix + ["status"]).map { shellEscape($0) }.joined(separator: " ")
        let cmd = "cd \(shellEscape(runner.workdir)) && \(statusCmd) 2>/dev/null || true"
        let out = shellOutput(cmd)
        guard out.code == 0 else { return [] }
        var result: [Int32] = []
        for line in out.output.split(separator: "\n").map({ String($0) }) {
            let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
            guard trimmed.hasPrefix("RUNNING ") else { continue }
            let parts = trimmed.split(separator: " ")
            for part in parts {
                let token = String(part)
                guard token.hasPrefix("pid=") else { continue }
                let pidRaw = String(token.dropFirst(4))
                guard let pid = Int32(pidRaw) else { continue }
                if kill(pid, 0) == 0 || errno == EPERM {
                    result.append(pid)
                }
            }
        }
        return result
    }

    private func runningChannelType(pid: Int32) -> ChannelType? {
        let out = shellOutput("ps eww -p \(pid)")
        guard out.code == 0 else { return nil }
        for token in out.output.split(separator: " ") {
            let item = token.trimmingCharacters(in: .whitespacesAndNewlines)
            if item.hasPrefix("CHANNEL_TYPE=") {
                let raw = String(item.dropFirst("CHANNEL_TYPE=".count)).trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
                return ChannelType(rawValue: raw)
            }
        }
        return nil
    }

    private func refreshSelectedSessionChat() {
        guard let sessionKey = selectedSessionKey, !sessionKey.isEmpty else {
            chatMessages = []
            timelineByMsgId = [:]
            log("chat refresh skipped reason=no_selected_session")
            return
        }
        let baseSessionKey = sessionKey.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? sessionKey
        let t0 = Date()
        log("chat refresh start session_key=\(sessionKey)")
        let overlay = localOverlayMessagesBySession[sessionKey, default: []]
        runInBackground { [weak self] in
            guard let self else { return }
            let res = self.cagJSON("messages", args: ["--session-key", baseSessionKey], timeoutSec: 8)
            var persisted: [ChatMessage] = []
            var timeline: [String: [ProcessEvent]] = [:]
            if let node = res.json,
               let ok = node["ok"] as? Bool, ok {
                if let items = node["messages"] as? [[String: Any]] {
                    persisted = items.compactMap { item in
                        let id = ((item["id"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                        if id.isEmpty { return nil }
                        return ChatMessage(
                            id: id,
                            sourceMsgId: (item["source_msg_id"] as? String) ?? id,
                            role: (item["role"] as? String) ?? "assistant",
                            text: (item["text"] as? String) ?? "",
                            time: (item["time"] as? String) ?? ISO8601DateFormatter().string(from: Date()),
                            deliveryStatus: nil,
                            statusDetail: (item["status_detail"] as? String) ?? ""
                        )
                    }
                }
                if let entries = node["timeline"] as? [[String: Any]] {
                    for entry in entries {
                        let msgID = ((entry["msg_id"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                        if msgID.isEmpty { continue }
                        let events = (entry["events"] as? [[String: Any]] ?? []).compactMap { ev -> ProcessEvent? in
                            let id = ((ev["id"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                            if id.isEmpty { return nil }
                            return ProcessEvent(
                                id: id,
                                time: (ev["time"] as? String) ?? ISO8601DateFormatter().string(from: Date()),
                                title: (ev["title"] as? String) ?? "",
                                detail: (ev["detail"] as? String) ?? ""
                            )
                        }
                        if !events.isEmpty {
                            timeline[msgID] = events
                        }
                    }
                }
            }
            let merged = self.mergedMessages(persisted: persisted, overlay: overlay)
            self.onMain {
                guard self.selectedSessionKey == sessionKey else { return }
                self.timelineByMsgId = timeline
                self.chatMessages = merged
            }
            let ms = Int(Date().timeIntervalSince(t0) * 1000)
            self.log("chat refresh done session_key=\(sessionKey) persisted=\(persisted.count) overlay=\(overlay.count) merged=\(merged.count) elapsed_ms=\(ms)")
        }
    }

    private func summarizeToolCalls(_ raw: Any?) -> String {
        guard let arr = raw as? [[String: Any]], !arr.isEmpty else { return "" }
        let parts = arr.prefix(6).map { call -> String in
            let title = (call["title"] as? String) ?? (call["tool_call_id"] as? String) ?? "tool"
            let status = (call["status"] as? String) ?? "unknown"
            return "\(title):\(status)"
        }
        return parts.joined(separator: ", ")
    }

    func setChannel(_ channel: ChannelType) {
        selectedChannel = channel
        UserDefaults.standard.set(channel.rawValue, forKey: channelDefaultsKey)
        writeEnvValue("CHANNEL_TYPE", value: channel.rawValue)
        refreshHealthChecksAsync()
        refreshStatusAsync()
    }

    func refreshStatus() {
        let t0 = Date()
        let res = cagJSON("status")
        guard let node = res.json else {
            onMain { [weak self] in
                guard let self else { return }
                self.statusText = "Unknown"
                self.activeChannelText = self.selectedChannel.title
                self.detailText = "Status command failed.\n\(res.raw.trimmingCharacters(in: .whitespacesAndNewlines))"
            }
            let ms = Int(Date().timeIntervalSince(t0) * 1000)
            log("refresh result kind=status status=parse_failed elapsed_ms=\(ms)")
            return
        }
        let status = (node["status"] as? String) ?? "unknown"
        let channelRaw = (node["channel"] as? String) ?? selectedChannel.rawValue
        let channel = ChannelType(rawValue: channelRaw) ?? selectedChannel
        let nodeLog = ((node["log_file"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        onMain { [weak self] in
            guard let self else { return }
            if !nodeLog.isEmpty {
                self.currentLogFile = nodeLog
            } else if self.currentLogFile.isEmpty {
                self.currentLogFile = self.cfg.logFile
            }
            self.activeChannelText = channel.title
            if status == "running" {
                self.statusText = "Running"
                let pidPart = (node["pid"] as? Int).map { "PID \($0)\n" } ?? ""
                let lockPart = (node["lock_file"] as? String).map { "Lock: \($0)\n" } ?? ""
                let logPart = self.currentLogFile
                self.detailText = "\(pidPart)Channel: \(channel.title)\n\(lockPart)Log: \(logPart)"
            } else {
                self.statusText = "Stopped"
                let lockPart = (node["lock_file"] as? String).map { "\nLock: \($0)" } ?? ""
                self.detailText = "Channel: \(channel.title)\nLog: \(self.currentLogFile)\(lockPart)"
            }
        }
        let ms = Int(Date().timeIntervalSince(t0) * 1000)
        log("refresh result kind=status status=\(status) elapsed_ms=\(ms)")
    }

    func autoStartOnLaunch() {
        if didAutoStartOnLaunch {
            return
        }
        didAutoStartOnLaunch = true
        refreshHealthChecks()
        if hasHealthFailures() {
            statusText = "Blocked"
            detailText = "Fix health issues first, then start gateway."
            return
        }
        refreshStatus()
        if statusText != "Running" {
            start()
        }
    }

    func refreshSessions() {
        let t0 = Date()
        let sessionsResult = cagJSON("sessions", args: ["--limit", "200"])
        if let node = sessionsResult.json,
           let ok = node["ok"] as? Bool, ok,
           let items = node["items"] as? [[String: Any]] {
            var built: [SessionEntry] = []
            for item in items {
                let sessionKey = (item["session_key"] as? String) ?? ""
                if sessionKey.isEmpty { continue }
                let senderName = ((item["sender_name"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                let senderID = ((item["sender"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                built.append(
                    SessionEntry(
                        sessionKey: sessionKey,
                        sessionId: (item["session_id"] as? String) ?? "-",
                        channel: (item["channel"] as? String) ?? "-",
                        senderId: senderID.isEmpty ? "-" : senderID,
                        sender: senderName.isEmpty ? (senderID.isEmpty ? "-" : senderID) : senderName,
                        threadId: (item["thread_id"] as? String) ?? "-",
                        lastText: (item["last_message"] as? String) ?? "",
                        lastTime: (item["last_time"] as? String) ?? "",
                        latest: (item["latest"] as? Bool) ?? false
                    )
                )
            }
            onMain { [weak self] in
                guard let self else { return }
                self.sessions = built.filter { self.shouldShowSession($0) }
                if let selected = self.selectedSessionKey, !self.sessions.contains(where: { $0.sessionKey == selected }) {
                    self.selectedSessionKey = nil
                }
                if self.selectedSessionKey == nil {
                    self.selectedSessionKey = self.sessions.first(where: { $0.latest })?.sessionKey ?? self.sessions.first?.sessionKey
                }
                self.refreshSelectedSessionChat()
            }
            let ms = Int(Date().timeIntervalSince(t0) * 1000)
            log("refresh result kind=sessions status=ok count=\(built.count) elapsed_ms=\(ms)")
            return
        }

        // GUI session list is CLI-driven only.
        onMain { [weak self] in
            self?.sessions = []
            self?.selectedSessionKey = nil
            self?.chatMessages = []
            self?.timelineByMsgId = [:]
        }
        let ms = Int(Date().timeIntervalSince(t0) * 1000)
        log("refresh result kind=sessions status=empty elapsed_ms=\(ms)")
        return
    }

    func selectSession(_ key: String?) {
        selectedSessionKey = key
        refreshSelectedSessionChat()
    }

    private func selectedSessionEntry() -> SessionEntry? {
        guard let key = selectedSessionKey else { return nil }
        return sessions.first(where: { $0.sessionKey == key })
    }

    private func mergedMessages(persisted: [ChatMessage], overlay: [ChatMessage]) -> [ChatMessage] {
        var merged = persisted
        for msg in overlay {
            if !merged.contains(where: { $0.id == msg.id }) {
                merged.append(msg)
            }
        }
        return merged.sorted { $0.time < $1.time }
    }

    private func appendOverlayMessage(_ msg: ChatMessage, sessionKey: String) {
        localOverlayMessagesBySession[sessionKey, default: []].append(msg)
        if selectedSessionKey == sessionKey {
            chatMessages.append(msg)
        }
    }

    private func removeOverlayMessage(sessionKey: String, messageId: String) {
        var overlay = localOverlayMessagesBySession[sessionKey, default: []]
        overlay.removeAll { $0.id == messageId }
        localOverlayMessagesBySession[sessionKey] = overlay
        chatMessages.removeAll { $0.id == messageId }
    }

    private func updateOverlayMessage(
        sessionKey: String,
        messageId: String,
        deliveryStatus: MessageDeliveryStatus,
        statusDetail: String
    ) {
        var overlay = localOverlayMessagesBySession[sessionKey, default: []]
        if let idx = overlay.firstIndex(where: { $0.id == messageId }) {
            let old = overlay[idx]
            overlay[idx] = ChatMessage(
                id: old.id,
                sourceMsgId: old.sourceMsgId,
                role: old.role,
                text: old.text,
                time: old.time,
                deliveryStatus: deliveryStatus,
                statusDetail: statusDetail
            )
            localOverlayMessagesBySession[sessionKey] = overlay
        }
        if let idx = chatMessages.firstIndex(where: { $0.id == messageId }) {
            let old = chatMessages[idx]
            chatMessages[idx] = ChatMessage(
                id: old.id,
                sourceMsgId: old.sourceMsgId,
                role: old.role,
                text: old.text,
                time: old.time,
                deliveryStatus: deliveryStatus,
                statusDetail: statusDetail
            )
        }
    }

    private func localChatTimeoutSec() -> TimeInterval {
        let raw = (envValue("AGENT_TIMEOUT_SEC") ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        guard let parsed = Int(raw), parsed > 0 else {
            return 120
        }
        return TimeInterval(max(30, min(parsed, 3600)))
    }

    private func extractLastJSONLine(_ text: String) -> String? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("{"), trimmed.hasSuffix("}") {
            return trimmed
        }

        let lines = text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        if lines.isEmpty {
            return nil
        }
        var lastValid: String?
        for start in 0..<lines.count {
            var balance = 0
            var started = false
            for end in start..<lines.count {
                for ch in lines[end] {
                    if ch == "{" {
                        balance += 1
                        started = true
                    } else if ch == "}" {
                        balance -= 1
                    }
                }
                if !started { continue }
                if balance < 0 { break }
                if balance == 0 {
                    let candidate = lines[start...end].joined(separator: "\n")
                        .trimmingCharacters(in: .whitespacesAndNewlines)
                    guard candidate.hasPrefix("{"), candidate.hasSuffix("}") else {
                        break
                    }
                    guard let data = candidate.data(using: .utf8) else {
                        break
                    }
                    if (try? JSONSerialization.jsonObject(with: data) as? [String: Any]) != nil {
                        lastValid = candidate
                    }
                    break
                }
            }
        }
        return lastValid
    }

    private func parseLocalCommand(_ text: String) -> (cmd: String, payload: String)? {
        let raw = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard raw.hasPrefix("/") else { return nil }
        let parts = raw.split(maxSplits: 1, omittingEmptySubsequences: true, whereSeparator: \.isWhitespace)
        guard let cmdPart = parts.first else { return nil }
        let cmd = String(cmdPart).lowercased()
        guard cmd == "/clear" || cmd == "/new" else { return nil }
        let payload = parts.count > 1 ? String(parts[1]).trimmingCharacters(in: .whitespacesAndNewlines) : ""
        return (cmd, payload)
    }

    private func clearSessionMapping(baseSessionKey: String) -> Bool {
        let res = cagJSON("session-clear", args: ["--session-key", baseSessionKey])
        guard let node = res.json else { return false }
        return (node["ok"] as? Bool) ?? false
    }

    private func appendLocalActionMessage(_ text: String, sessionKey: String) {
        let msgId = "local-sys-\(Int(Date().timeIntervalSince1970 * 1000))"
        let msg = ChatMessage(
            id: msgId,
            sourceMsgId: msgId,
            role: "system",
            text: text,
            time: ISO8601DateFormatter().string(from: Date()),
            deliveryStatus: .action,
            statusDetail: ""
        )
        appendOverlayMessage(msg, sessionKey: sessionKey)
    }

    func sendLocalChat() {
        var text = localDraftText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        guard !localSending else { return }
        guard let session = selectedSessionEntry() else {
            detailText = "Select a session first."
            return
        }
        let selectedSessionKey = session.sessionKey
        let sendFingerprint = "\(selectedSessionKey)|\(text)"
        let now = Date()
        if sendFingerprint == lastLocalSendFingerprint, now.timeIntervalSince(lastLocalSendAt) < 1.2 {
            detailText = "Ignored duplicate local send."
            return
        }
        lastLocalSendFingerprint = sendFingerprint
        lastLocalSendAt = now
        let baseSessionKey = session.sessionKey.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? session.sessionKey

        if let cmd = parseLocalCommand(text) {
            let cleared = clearSessionMapping(baseSessionKey: baseSessionKey)
            if cmd.cmd == "/clear" {
                appendLocalActionMessage(
                    cleared ? "Action /clear: session mapping reset." : "Action /clear failed: cannot update state file.",
                    sessionKey: selectedSessionKey
                )
                detailText = cleared ? "Session mapping cleared." : "Failed to clear session mapping."
                localDraftText = ""
                refreshSessionsAsync()
                return
            }
            appendLocalActionMessage(
                cleared ? "Action /new: session reset." : "Action /new warning: reset failed, sending anyway.",
                sessionKey: selectedSessionKey
            )
            detailText = cleared ? "New session started." : "Could not reset old session; continuing send."
            if cmd.payload.isEmpty {
                localDraftText = ""
                refreshSessionsAsync()
                return
            }
            text = cmd.payload
        }

        localSending = true

        let userMsgId = "local-u-\(Int(Date().timeIntervalSince1970 * 1000))"
        let nowIso = ISO8601DateFormatter().string(from: Date())
        let localUser = ChatMessage(
            id: userMsgId,
            sourceMsgId: userMsgId,
            role: "user",
            text: text,
            time: nowIso,
            deliveryStatus: .sending,
            statusDetail: ""
        )
        appendOverlayMessage(localUser, sessionKey: selectedSessionKey)
        localDraftText = ""
        if baseSessionKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            localSending = false
            detailText = "Send failed: missing session key."
            updateOverlayMessage(
                sessionKey: selectedSessionKey,
                messageId: userMsgId,
                deliveryStatus: .failed,
                statusDetail: "missing session key"
            )
            return
        }
        let timeout = localChatTimeoutSec()
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self else { return }
            let sendArgs = ["--session-key", baseSessionKey, "--text", text]
            let result = self.cagJSON("send", args: sendArgs, timeoutSec: timeout)
            DispatchQueue.main.async {
                self.localSending = false
                guard let node = result.json else {
                    self.updateOverlayMessage(
                        sessionKey: selectedSessionKey,
                        messageId: userMsgId,
                        deliveryStatus: .failed,
                        statusDetail: "invalid CLI response"
                    )
                    self.detailText = "Send failed: invalid CLI response."
                    return
                }
                let ok = (node["ok"] as? Bool) ?? false
                if ok {
                    self.updateOverlayMessage(
                        sessionKey: selectedSessionKey,
                        messageId: userMsgId,
                        deliveryStatus: .sent,
                        statusDetail: ""
                    )
                    self.removeOverlayMessage(sessionKey: selectedSessionKey, messageId: userMsgId)
                    let elapsed = (node["elapsed_sec"] as? Int) ?? 0
                    self.detailText = elapsed > 0 ? "Session processed (\(elapsed)s)." : "Session processed."
                    self.refreshSessionsAsync()
                    self.refreshSelectedSessionChat()
                    return
                }
                let nestedErr = ((node["error"] as? [String: Any])?["message"] as? String) ?? ""
                let plainErr = (node["error"] as? String) ?? ""
                let errText = nestedErr.isEmpty ? (plainErr.isEmpty ? "send failed" : plainErr) : nestedErr
                self.updateOverlayMessage(
                    sessionKey: selectedSessionKey,
                    messageId: userMsgId,
                    deliveryStatus: .failed,
                    statusDetail: errText
                )
                self.detailText = "Send failed: \(errText)"
            }
        }
    }

    func deleteSelectedSession() {
        guard let key = selectedSessionKey else { return }
        deleteSession(key: key)
    }

    func deleteAllSessions() {
        let res = cagJSON("sessions-delete-all")
        if ((res.json?["ok"] as? Bool) ?? false) {
            for s in sessions {
                hideSessionKey(s.sessionKey)
            }
            saveHiddenSessionCutoffByKey()
            selectedSessionKey = nil
            refreshSessionsAsync()
            detailText = "Deleted all sessions."
        } else {
            detailText = "Delete failed: command failed."
        }
    }

    func deleteSession(key: String) {
        if key.contains("#") {
            hideSessionKey(key)
            saveHiddenSessionCutoffByKey()
            if selectedSessionKey == key {
                selectedSessionKey = nil
            }
            refreshSessionsAsync()
            detailText = "Deleted archived session segment from app list."
            return
        }
        let res = cagJSON("session-delete", args: ["--session-key", key])
        if ((res.json?["ok"] as? Bool) ?? false) {
            for session in sessions {
                if session.sessionKey == key || session.sessionKey.hasPrefix("\(key)#") {
                    hideSessionKey(session.sessionKey)
                }
            }
            saveHiddenSessionCutoffByKey()
            if selectedSessionKey == key {
                selectedSessionKey = nil
            }
            refreshSessionsAsync()
            detailText = "Deleted session: \(key)"
        } else {
            detailText = "Delete failed: command failed."
        }
    }

    func start() {
        refreshHealthChecks()
        if hasHealthFailures() {
            statusText = "Blocked"
            detailText = "Cannot start: unresolved health issues."
            return
        }
        writeEnvValue("CHANNEL_TYPE", value: selectedChannel.rawValue)
        let res = cagJSON("start")
        guard let node = res.json else {
            statusText = "Start failed"
            detailText = "Invalid CLI response.\n\(res.raw.trimmingCharacters(in: .whitespacesAndNewlines))"
            return
        }
        let ok = (node["ok"] as? Bool) ?? false
        if ok {
            refreshStatus()
            let pidText = (node["pid"] as? Int).map { "PID: \($0)\n" } ?? ""
            let logPath = ((node["log_file"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            if !logPath.isEmpty {
                currentLogFile = logPath
            }
            let shownLog = currentLogFile.isEmpty ? cfg.logFile : currentLogFile
            detailText = "\(pidText)Channel: \(activeChannelText)\nLog: \(shownLog)"
            return
        }
        statusText = "Start failed"
        let errorText = ((node["error"] as? [String: Any])?["message"] as? String) ?? "Unknown error"
        detailText = errorText
    }

    func stop() {
        let res = cagJSON("stop")
        guard let node = res.json else {
            statusText = "Stop failed"
            detailText = "Invalid CLI response.\n\(res.raw.trimmingCharacters(in: .whitespacesAndNewlines))"
            return
        }
        if ((node["ok"] as? Bool) ?? false) == false {
            statusText = "Stop failed"
            let errorText = ((node["error"] as? [String: Any])?["message"] as? String) ?? "Unknown error"
            detailText = errorText
            return
        }
        refreshStatus()
        detailText = "Gateway stopped."
    }

    func restart() {
        statusText = "Restarting"
        detailText = "Restarting gateway..."
        writeEnvValue("CHANNEL_TYPE", value: selectedChannel.rawValue)
        let res = cagJSON("restart")
        guard let node = res.json else {
            statusText = "Restart failed"
            detailText = "Invalid CLI response.\n\(res.raw.trimmingCharacters(in: .whitespacesAndNewlines))"
            return
        }
        if ((node["ok"] as? Bool) ?? false) == false {
            statusText = "Restart failed"
            let errorText = ((node["error"] as? [String: Any])?["message"] as? String) ?? "Unknown error"
            detailText = errorText
            return
        }
        refreshStatus()
        let pidText = (node["pid"] as? Int).map { "PID: \($0)\n" } ?? ""
        let logPath = ((node["log_file"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        if !logPath.isEmpty {
            currentLogFile = logPath
        }
        let shownLog = currentLogFile.isEmpty ? cfg.logFile : currentLogFile
        detailText = "\(pidText)Gateway restarted.\nLog: \(shownLog)"
    }

    func ensureGatewaydForGUI() {
        runInBackground { [weak self] in
            guard let self else { return }
            let res = self.cagJSON("gatewayd-up", timeoutSec: 5)
            if res.code != 0 {
                self.log("gatewayd-up failed code=\(res.code) raw=\(res.raw)")
            } else {
                self.log("gatewayd-up ok")
            }
        }
    }

    func shutdownGatewaydForGUI() {
        runInBackground { [weak self] in
            guard let self else { return }
            let res = self.cagJSON("gatewayd-down", timeoutSec: 5)
            if res.code != 0 {
                self.log("gatewayd-down failed code=\(res.code) raw=\(res.raw)")
            } else {
                self.log("gatewayd-down ok")
            }
        }
    }

    func latestLogTail(lines: Int = 120) -> String {
        let target = currentLogFile.isEmpty ? cfg.logFile : currentLogFile
        guard FileManager.default.fileExists(atPath: target),
              let content = try? String(contentsOfFile: target, encoding: .utf8) else {
            return "Log tail failed: file not found.\n\(target)"
        }
        let recent = content.split(separator: "\n", omittingEmptySubsequences: false).suffix(max(1, lines))
        let preview = recent.joined(separator: "\n")
        return "Log: \(target)\n--- tail \(lines) ---\n\(preview)"
    }

    private func shellEscape(_ raw: String) -> String {
        if raw.isEmpty {
            return "''"
        }
        return "'" + raw.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }
}

struct Pill: View {
    let text: String
    let color: Color

    var body: some View {
        Text(text)
            .font(.system(size: 12, weight: .semibold))
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(color.opacity(0.14), in: Capsule())
            .overlay(Capsule().stroke(color.opacity(0.35), lineWidth: 1))
    }
}

struct HealthRow: View {
    let item: HealthCheckItem
    let onRepair: (RepairAction) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: item.ok ? "checkmark.circle.fill" : "xmark.octagon.fill")
                    .foregroundStyle(item.ok ? .green : .red)
                Text(item.title)
                    .font(.system(size: 12, weight: .semibold))
                Spacer()
                if !item.ok, let action = item.repairAction {
                    Button("Repair") { onRepair(action) }
                }
            }
            Text(item.detail)
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
        }
        .padding(.vertical, 2)
    }
}

struct SessionRow: View {
    let session: SessionEntry

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(session.sender == "-" ? session.sessionKey : session.sender)
                    .font(.system(size: 13, weight: .semibold))
                Spacer()
                Text(session.channel)
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(.secondary)
            }
            Text(session.lastText)
                .font(.system(size: 12))
                .lineLimit(2)
                .foregroundStyle(.secondary)
            Text("sid: \(session.sessionId)")
                .font(.system(size: 10))
                .foregroundStyle(.tertiary)
        }
        .padding(.vertical, 4)
    }
}

struct ChatBubble: View {
    let message: ChatMessage
    let onAssistantTap: (ChatMessage) -> Void
    @State private var hovering = false

    private var isUser: Bool { message.role == "user" }
    private var isSystem: Bool { message.role == "system" }

    private var deliveryText: String {
        switch message.deliveryStatus {
        case .sending: return "Sending..."
        case .sent: return "Sent"
        case .failed: return "Failed"
        case .action: return "Action"
        case .none: return ""
        }
    }

    private var deliveryColor: Color {
        switch message.deliveryStatus {
        case .sending: return .orange
        case .failed: return .red
        case .action: return .secondary
        case .sent, .none: return .gray
        }
    }

    @ViewBuilder
    private var bubbleContent: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(isUser ? "You" : "Assistant")
                .font(.system(size: 10, weight: .semibold))
                .foregroundStyle(.secondary)
            Text(message.text)
                .font(.system(size: 12))
                .textSelection(.enabled)
            if isUser, !deliveryText.isEmpty {
                Text(deliveryText)
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(deliveryColor)
            }
            if isUser, message.deliveryStatus == .failed, !message.statusDetail.isEmpty {
                Text(message.statusDetail)
                    .font(.system(size: 10))
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
            if !message.time.isEmpty {
                Text(message.time)
                    .font(.system(size: 10))
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(10)
    }

    var body: some View {
        Group {
            if isSystem {
                HStack {
                    Spacer()
                    Text(message.text)
                        .font(.system(size: 11, weight: .medium))
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 6)
                        .background(Color.gray.opacity(0.12), in: Capsule())
                    Spacer()
                }
            } else {
                HStack {
                    if isUser { Spacer(minLength: 30) }
                    if isUser {
                        bubbleContent
                            .background(Color.accentColor.opacity(0.16), in: RoundedRectangle(cornerRadius: 10))
                    } else {
                        Button {
                            onAssistantTap(message)
                        } label: {
                            bubbleContent
                                .overlay(
                                    RoundedRectangle(cornerRadius: 10)
                                        .stroke(hovering ? Color.accentColor.opacity(0.45) : Color.clear, lineWidth: 1)
                                )
                        }
                        .buttonStyle(.plain)
                        .background((hovering ? Color.gray.opacity(0.20) : Color.gray.opacity(0.14)), in: RoundedRectangle(cornerRadius: 10))
                        .onHover { inside in
                            hovering = inside
                            if inside {
                                NSCursor.pointingHand.set()
                            } else {
                                NSCursor.arrow.set()
                            }
                        }
                    }
                    if !isUser { Spacer(minLength: 30) }
                }
            }
        }
    }
}

struct ProcessTimelineView: View {
    let message: ChatMessage
    let events: [ProcessEvent]

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("AI Process Detail")
                .font(.headline)
            Text(message.text)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .lineLimit(3)
            Divider()
            if events.isEmpty {
                Text("No detailed process events recorded for this answer.")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 10) {
                        ForEach(events) { evt in
                            VStack(alignment: .leading, spacing: 4) {
                                HStack {
                                    Text(evt.title).font(.system(size: 12, weight: .semibold))
                                    Spacer()
                                    if !evt.time.isEmpty {
                                        Text(evt.time).font(.system(size: 10)).foregroundStyle(.tertiary)
                                    }
                                }
                                if !evt.detail.isEmpty {
                                    Text(evt.detail).font(.system(size: 11)).foregroundStyle(.secondary)
                                }
                            }
                            .padding(8)
                            .background(Color.gray.opacity(0.10), in: RoundedRectangle(cornerRadius: 8))
                        }
                    }
                }
            }
        }
        .padding(16)
        .frame(width: 560, height: 440)
    }
}

struct LogTailView: View {
    let content: String

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Gateway Log Tail")
                .font(.headline)
            ScrollView {
                Text(content)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(10)
            .background(Color.gray.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
        }
        .padding(16)
        .frame(width: 760, height: 520)
    }
}

struct ConfigView: View {
    @ObservedObject var controller: GatewayController

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Config")
                    .font(.headline)
                Spacer()
            }
            Picker("Channel", selection: Binding(
                get: { controller.selectedChannel },
                set: { controller.setChannel($0) }
            )) {
                ForEach(ChannelType.allCases) { channel in
                    Text(channel.title).tag(channel)
                }
            }
            .pickerStyle(.segmented)
            Text("Session Tips: /new to start a fresh session, /clear to reset current session mapping.")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            Divider()
            Text("Health Board").font(.headline)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 8) {
                    ForEach(controller.healthChecks) { item in
                        HealthRow(item: item) { action in
                            controller.runRepair(action)
                        }
                    }
                }
            }
        }
        .padding(16)
        .frame(width: 560, height: 520)
    }
}

struct ContentView: View {
    @StateObject private var controller: GatewayController
    private let refreshTimer = Timer.publish(every: 2.0, on: .main, in: .common).autoconnect()
    @State private var showConfig = false
    @State private var timelineMessage: ChatMessage?
    @State private var showLogTail = false
    @State private var logTailText = ""
    @State private var refreshTick: Int = 0

    init(controller: GatewayController) {
        _controller = StateObject(wrappedValue: controller)
    }

    private var statusColor: Color {
        controller.statusText == "Running" ? .green : (controller.statusText == "Blocked" ? .orange : .gray)
    }

    private func scrollChatToLatest(_ proxy: ScrollViewProxy) {
        guard let last = controller.chatMessages.last else { return }
        DispatchQueue.main.async {
            withAnimation(.easeOut(duration: 0.2)) {
                proxy.scrollTo(last.id, anchor: .bottom)
            }
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Image(systemName: "app.badge.checkmark")
                    .font(.system(size: 18, weight: .semibold))
                Text("CLI Agent Gateway")
                    .font(.title3.weight(.semibold))
                Spacer()
                Pill(text: controller.statusText, color: statusColor)
                Pill(text: "Active: \(controller.activeChannelText)", color: .blue)
                Button("Start") { controller.start() }
                    .keyboardShortcut(.return, modifiers: [])
                    .disabled(controller.statusText == "Running")
                Button("Stop") { controller.stop() }
                    .disabled(controller.statusText != "Running")
                Button("Restart") { controller.restart() }
                    .disabled(controller.statusText == "Blocked")
                Button("Tail Logs") {
                    logTailText = controller.latestLogTail()
                    showLogTail = true
                }
                Button("Config") { showConfig = true }
            }
            .padding(.horizontal, 18)
            .padding(.vertical, 14)
            .background(.ultraThinMaterial)

            HStack(spacing: 0) {
                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Text("Sessions")
                            .font(.title3.weight(.semibold))
                        Spacer()
                        Button("Delete Selected") { controller.deleteSelectedSession() }
                            .disabled(controller.selectedSessionKey == nil)
                        Button("Delete All") { controller.deleteAllSessions() }
                            .foregroundStyle(.red)
                    }
                    ScrollView {
                        LazyVStack(spacing: 8) {
                            ForEach(controller.sessions) { session in
                                Button {
                                    controller.selectSession(session.sessionKey)
                                } label: {
                                    SessionRow(session: session)
                                        .padding(10)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                        .background(
                                            (controller.selectedSessionKey == session.sessionKey
                                                ? Color.accentColor.opacity(0.18)
                                                : Color.gray.opacity(0.10)),
                                            in: RoundedRectangle(cornerRadius: 10)
                                        )
                                }
                                .buttonStyle(.plain)
                                .contextMenu {
                                    Button("Delete Session") {
                                        controller.deleteSession(key: session.sessionKey)
                                    }
                                }
                            }
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
                .padding(14)
                .frame(minWidth: 380, idealWidth: 420)

                Divider()

                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Text("Chat")
                            .font(.title3.weight(.semibold))
                        Spacer()
                        Text(controller.detailText)
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                    }

                    ScrollViewReader { proxy in
                        ScrollView {
                            LazyVStack(spacing: 10) {
                                if controller.chatMessages.isEmpty {
                                    Text("Select a session to view chat history.")
                                        .font(.system(size: 12))
                                        .foregroundStyle(.secondary)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                } else {
                                    ForEach(controller.chatMessages.suffix(200)) { msg in
                                        ChatBubble(message: msg) { tapped in
                                            timelineMessage = tapped
                                        }
                                            .id(msg.id)
                                    }
                                }
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .onAppear {
                            scrollChatToLatest(proxy)
                        }
                        .onChange(of: controller.chatMessages.count) { _, _ in
                            scrollChatToLatest(proxy)
                        }
                    }

                    HStack(spacing: 8) {
                        TextField("Type here to chat locally in this session...", text: $controller.localDraftText)
                            .textFieldStyle(.roundedBorder)
                            .disabled(controller.selectedSessionKey == nil)
                            .onSubmit {
                                controller.sendLocalChat()
                            }
                        Button(controller.localSending ? "Sending..." : "Send") {
                            controller.sendLocalChat()
                        }
                        .disabled(
                            controller.selectedSessionKey == nil
                                || controller.localSending
                                || controller.localDraftText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        )
                    }
                }
                .padding(14)
                .frame(minWidth: 620, idealWidth: 720)
            }
        }
        .frame(width: 1140, height: 700)
        .onAppear {
            GUILogger.shared.log("view onAppear bootstrap refresh")
            controller.ensureGatewaydForGUI()
            controller.refreshHealthChecksAsync()
            controller.refreshStatusAsync()
            controller.refreshSessionsAsync()
        }
        .onReceive(refreshTimer) { _ in
            refreshTick += 1
            controller.refreshStatusAsync()
            if !controller.localSending && (refreshTick % 3 == 0) {
                controller.refreshSessionsAsync()
            }
        }
        .sheet(isPresented: $showConfig) {
            ConfigView(controller: controller)
        }
        .sheet(item: $timelineMessage) { msg in
            ProcessTimelineView(message: msg, events: controller.timeline(for: msg))
        }
        .sheet(isPresented: $showLogTail) {
            LogTailView(content: logTailText)
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.willTerminateNotification)) { _ in
            controller.shutdownGatewaydForGUI()
        }
    }
}

final class AppBootstrap: ObservableObject {
    let controller: GatewayController?

    init() {
        controller = try? GatewayController()
        if controller == nil {
            GUILogger.shared.log("bootstrap controller init failed")
        } else {
            GUILogger.shared.log("bootstrap controller init ok")
        }
    }
}

@main
struct CLIAppMain: App {
    @StateObject private var bootstrap = AppBootstrap()

    var body: some Scene {
        WindowGroup {
            if let controller = bootstrap.controller {
                ContentView(controller: controller)
            } else {
                VStack(spacing: 10) {
                    Text("Failed to load app configuration.")
                    Text("Rebuild app from repository scripts.")
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)
                }
                .padding(20)
                .frame(width: 420, height: 160)
            }
        }
    }
}
