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
    case installPython
    case installCodexACP
    case installIMsg
    case installDingTalkStream
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
    let sender: String
    let threadId: String
    let lastText: String
    let lastTime: String

    var id: String { sessionKey }
}

struct ChatMessage: Identifiable {
    let id: String
    let sourceMsgId: String
    let role: String
    let text: String
    let time: String
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

final class GatewayController: ObservableObject {
    @Published var statusText: String = "Checking status..."
    @Published var detailText: String = ""
    @Published var selectedChannel: ChannelType
    @Published var sessions: [SessionEntry] = []
    @Published var selectedSessionKey: String?
    @Published var recentSessionKeys: [String] = []
    @Published var chatMessages: [ChatMessage] = []
    @Published var healthChecks: [HealthCheckItem] = []
    @Published var timelineByMsgId: [String: [ProcessEvent]] = [:]

    private let cfg: GatewayConfig
    private let channelDefaultsKey = "gateway.selected_channel"
    private var didAutoStartOnLaunch = false

    init() throws {
        cfg = try GatewayController.loadConfig()
        selectedChannel = GatewayController.loadSavedChannel(defaultChannel: GatewayController.detectEnvChannel(repoRoot: cfg.repoRoot))
        refreshHealthChecks()
        refreshStatus()
        refreshSessions()
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

    private func shellOutput(_ command: String) -> (code: Int32, output: String) {
        let proc = Process()
        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = pipe
        proc.executableURL = URL(fileURLWithPath: "/bin/zsh")
        proc.arguments = ["-lc", command]
        do {
            try proc.run()
            proc.waitUntilExit()
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            let text = String(data: data, encoding: .utf8) ?? ""
            return (proc.terminationStatus, text)
        } catch {
            return (127, error.localizedDescription)
        }
    }

    private func commandExists(_ cmd: String) -> Bool {
        let esc = cmd.replacingOccurrences(of: "'", with: "'\\''")
        return shellOutput("command -v '\(esc)' >/dev/null 2>&1").code == 0
    }

    private func hasHealthFailures() -> Bool {
        healthChecks.contains(where: { !$0.ok })
    }

    func timeline(for message: ChatMessage) -> [ProcessEvent] {
        timelineByMsgId[message.sourceMsgId, default: []]
    }

    func refreshHealthChecks() {
        var checks: [HealthCheckItem] = []

        let envExists = FileManager.default.fileExists(atPath: envPath)
        checks.append(
            HealthCheckItem(
                id: "env",
                title: ".env configuration",
                ok: envExists,
                detail: envExists ? "Found: \(envPath)" : "Missing: \(envPath)",
                repairAction: envExists ? nil : .setupEnv
            )
        )

        let pyOK = commandExists("python3")
        checks.append(
            HealthCheckItem(
                id: "python3",
                title: "Python runtime",
                ok: pyOK,
                detail: pyOK ? "python3 is available in PATH." : "python3 not found in PATH.",
                repairAction: pyOK ? nil : .installPython
            )
        )

        let acpCmd = (envValue("ACP_AGENT_CMD")?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty == false)
            ? envValue("ACP_AGENT_CMD")!.trimmingCharacters(in: .whitespacesAndNewlines)
            : "codex-acp"
        let acpOK = commandExists(acpCmd)
        checks.append(
            HealthCheckItem(
                id: "acp",
                title: "ACP agent command",
                ok: acpOK,
                detail: acpOK ? "\(acpCmd) is available." : "\(acpCmd) not found. Required by gateway.",
                repairAction: acpOK ? nil : .installCodexACP
            )
        )

        if selectedChannel == .imessage {
            let imsgOK = commandExists("imsg")
            checks.append(
                HealthCheckItem(
                    id: "imsg",
                    title: "iMessage CLI (imsg)",
                    ok: imsgOK,
                    detail: imsgOK ? "imsg is available." : "imsg not found. iMessage channel requires it.",
                    repairAction: imsgOK ? nil : .installIMsg
                )
            )
        }

        if selectedChannel == .dingtalk {
            let dt = shellOutput("python3 -c 'import dingtalk_stream' >/dev/null 2>&1")
            let dtOK = dt.code == 0
            checks.append(
                HealthCheckItem(
                    id: "dingtalk_stream",
                    title: "dingtalk-stream Python package",
                    ok: dtOK,
                    detail: dtOK ? "dingtalk-stream is installed." : "Missing dingtalk-stream package.",
                    repairAction: dtOK ? nil : .installDingTalkStream
                )
            )
        }

        healthChecks = checks
    }

    func runRepair(_ action: RepairAction) {
        switch action {
        case .setupEnv:
            let cmd = "cd \(shellEscape(cfg.repoRoot)) && make config"
            let esc = cmd.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
            let script = "tell application \"Terminal\" to do script \"\(esc)\""
            _ = shellOutput("osascript -e \"\(script.replacingOccurrences(of: "\"", with: "\\\""))\"")
            detailText = "Opened setup wizard in Terminal. Complete it, then checks will pass."

        case .installPython:
            if commandExists("brew") {
                let out = shellOutput("brew install python")
                detailText = out.code == 0 ? "Python install completed." : "Python install failed:\n\(out.output)"
            } else {
                NSWorkspace.shared.open(URL(string: "https://www.python.org/downloads/")!)
                detailText = "Opened Python download page (Homebrew not found)."
            }

        case .installCodexACP:
            let cmd = "if command -v pipx >/dev/null 2>&1; then pipx install codex-acp || pipx upgrade codex-acp; else python3 -m pip install --user -U codex-acp; fi"
            let out = shellOutput(cmd)
            detailText = out.code == 0 ? "codex-acp install/upgrade completed." : "codex-acp repair failed:\n\(out.output)"

        case .installIMsg:
            NSWorkspace.shared.open(URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent("docs/IMESSAGE_SETUP.md"))
            detailText = "Opened iMessage setup guide. Install and configure imsg first."

        case .installDingTalkStream:
            let out = shellOutput("python3 -m pip install -U dingtalk-stream")
            detailText = out.code == 0 ? "dingtalk-stream install completed." : "dingtalk-stream install failed:\n\(out.output)"
        }
        refreshHealthChecks()
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
        return nil
    }

    private func channelFromProfile(_ profileAny: Any?) -> String {
        guard let profile = profileAny as? [String: Any], let channel = profile["channel"] as? String else {
            return ""
        }
        return channel
    }

    private func threadFromProfile(_ profileAny: Any?) -> String {
        guard let profile = profileAny as? [String: Any], let thread = profile["thread_id"] as? String else {
            return ""
        }
        return thread
    }

    private func buildSessionKey(channel: String, sender: String, threadId: String) -> String {
        let raw = "\(channel)|\(sender)|\(threadId.isEmpty ? "-" : threadId)"
        let digest = SHA256.hash(data: Data(raw.utf8))
        let hex = digest.map { String(format: "%02x", $0) }.joined()
        return "sess_" + String(hex.prefix(24))
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
        refreshHealthChecks()
        refreshStatus()
    }

    func refreshStatus() {
        if let pid = runningPID() {
            statusText = "Running"
            detailText = "PID \(pid)\nChannel: \(selectedChannel.title)\nLog: \(cfg.logFile)"
        } else {
            statusText = "Stopped"
            detailText = "Channel: \(selectedChannel.title)\nLog: \(cfg.logFile)"
        }
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
        if runningPID() == nil {
            start()
        }
    }

    func refreshSessions() {
        var sessionMap: [String: String] = [:]
        if let data = try? Data(contentsOf: URL(fileURLWithPath: cfg.stateFile)),
           let node = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let rawMap = node["session_map"] as? [String: Any] {
            for (k, v) in rawMap {
                sessionMap[k] = String(describing: v)
            }
        }

        typealias InboundMeta = (sender: String, text: String, channel: String, thread: String, ts: String)
        var inboundByMsgId: [String: InboundMeta] = [:]
        var sessionByKey: [String: InboundMeta] = [:]
        var sessionKeyByMsgId: [String: String] = [:]
        var chatBySession: [String: [ChatMessage]] = [:]
        var timelineByMsg: [String: [ProcessEvent]] = [:]
        var records: [[String: Any]] = []

        if let content = try? String(contentsOfFile: cfg.interactionLogFile, encoding: .utf8) {
            let lines = content.split(separator: "\n", omittingEmptySubsequences: true)
            for line in lines.suffix(5000) {
                guard
                    let data = line.data(using: .utf8),
                    let record = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                    let kind = record["kind"] as? String
                else {
                    continue
                }
                records.append(record)

                if kind == "inbound_received" {
                    guard let msgId = record["msg_id"] as? String else { continue }
                    let sender = (record["sender"] as? String) ?? ""
                    let text = (record["text"] as? String) ?? ""
                    let ts = (record["time"] as? String) ?? ""
                    let channel = channelFromProfile(record["user_profile"])
                    let thread = threadFromProfile(record["user_profile"])
                    inboundByMsgId[msgId] = (sender: sender, text: text, channel: channel, thread: thread, ts: ts)
                    let computedKey = buildSessionKey(channel: channel, sender: sender, threadId: thread)
                    sessionKeyByMsgId[msgId] = computedKey
                    if let prev = sessionByKey[computedKey] {
                        if ts >= prev.ts {
                            sessionByKey[computedKey] = (sender: sender, text: text, channel: channel, thread: thread, ts: ts)
                        }
                    } else {
                        sessionByKey[computedKey] = (sender: sender, text: text, channel: channel, thread: thread, ts: ts)
                    }
                }

                if kind == "trace",
                   let stage = record["stage"] as? String,
                   stage == "session_resolved",
                   let sessionKey = record["session_key"] as? String,
                   let msgId = record["msg_id"] as? String,
                   let inbound = inboundByMsgId[msgId] {
                    sessionKeyByMsgId[msgId] = sessionKey
                    if let prev = sessionByKey[sessionKey] {
                        if inbound.ts >= prev.ts {
                            sessionByKey[sessionKey] = inbound
                        }
                    } else {
                        sessionByKey[sessionKey] = inbound
                    }
                }
            }

            for record in records {
                guard let kind = record["kind"] as? String else { continue }

                if kind == "inbound_received",
                   let msgId = record["msg_id"] as? String,
                   let sessionKey = sessionKeyByMsgId[msgId] {
                    let text = ((record["text"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !text.isEmpty {
                        let ts = (record["time"] as? String) ?? ""
                        let msg = ChatMessage(id: "\(msgId)-u", sourceMsgId: msgId, role: "user", text: text, time: ts)
                        chatBySession[sessionKey, default: []].append(msg)
                        let e = ProcessEvent(
                            id: "\(msgId)-evt-in",
                            time: ts,
                            title: "User Message",
                            detail: text
                        )
                        timelineByMsg[msgId, default: []].append(e)
                    }
                }

                if kind == "trace",
                   let stage = record["stage"] as? String,
                   let msgId = record["msg_id"] as? String,
                   stage.hasPrefix("acp.") {
                    let ts = (record["time"] as? String) ?? ""
                    var details: [String] = []
                    for key in ["status", "title", "session_update", "session_id", "elapsed_sec", "raw_events"] {
                        if let val = record[key] {
                            details.append("\(key)=\(val)")
                        }
                    }
                    let e = ProcessEvent(
                        id: "\(msgId)-evt-\(timelineByMsg[msgId, default: []].count)-trace",
                        time: ts,
                        title: stage,
                        detail: details.joined(separator: " | ")
                    )
                    timelineByMsg[msgId, default: []].append(e)
                }

                if kind == "tool_progress_notify",
                   let msgId = record["msg_id"] as? String {
                    let ts = (record["time"] as? String) ?? ""
                    let title = ((record["title"] as? String) ?? "tool").trimmingCharacters(in: .whitespacesAndNewlines)
                    let status = ((record["status"] as? String) ?? "in_progress").trimmingCharacters(in: .whitespacesAndNewlines)
                    let text = ((record["text"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                    let e = ProcessEvent(
                        id: "\(msgId)-evt-\(timelineByMsg[msgId, default: []].count)-tool",
                        time: ts,
                        title: "Tool \(status)",
                        detail: text.isEmpty ? title : text
                    )
                    timelineByMsg[msgId, default: []].append(e)
                }

                if kind == "tool_trace",
                   let msgId = record["msg_id"] as? String {
                    let ts = (record["time"] as? String) ?? ""
                    let tools = (record["tools"] as? [String]) ?? []
                    let callsSummary = summarizeToolCalls(record["tool_calls"])
                    var detail = tools.isEmpty ? "" : tools.joined(separator: ", ")
                    if !callsSummary.isEmpty {
                        detail = detail.isEmpty ? callsSummary : "\(detail)\n\(callsSummary)"
                    }
                    let e = ProcessEvent(
                        id: "\(msgId)-evt-\(timelineByMsg[msgId, default: []].count)-trace2",
                        time: ts,
                        title: "Tools Used",
                        detail: detail
                    )
                    timelineByMsg[msgId, default: []].append(e)
                }

                if kind == "exec_finished",
                   let msgId = record["msg_id"] as? String,
                   let sessionKey = sessionKeyByMsgId[msgId] {
                    let text = ((record["summary"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !text.isEmpty {
                        let ts = (record["time"] as? String) ?? ""
                        let msg = ChatMessage(id: "\(msgId)-a", sourceMsgId: msgId, role: "assistant", text: text, time: ts)
                        chatBySession[sessionKey, default: []].append(msg)
                        let elapsed = String(describing: record["elapsed_sec"] ?? "")
                        let detail = elapsed.isEmpty ? "Completed." : "Completed in \(elapsed)s."
                        let e = ProcessEvent(
                            id: "\(msgId)-evt-\(timelineByMsg[msgId, default: []].count)-done",
                            time: ts,
                            title: "Completed",
                            detail: detail
                        )
                        timelineByMsg[msgId, default: []].append(e)
                    }
                }

                if kind == "exec_error",
                   let msgId = record["msg_id"] as? String,
                   let sessionKey = sessionKeyByMsgId[msgId] {
                    let text = ((record["error"] as? String) ?? "Execution error").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !text.isEmpty {
                        let ts = (record["time"] as? String) ?? ""
                        let msg = ChatMessage(id: "\(msgId)-e", sourceMsgId: msgId, role: "assistant", text: "Error: \(text)", time: ts)
                        chatBySession[sessionKey, default: []].append(msg)
                        let e = ProcessEvent(
                            id: "\(msgId)-evt-\(timelineByMsg[msgId, default: []].count)-err",
                            time: ts,
                            title: "Error",
                            detail: text
                        )
                        timelineByMsg[msgId, default: []].append(e)
                    }
                }
            }
        }

        var allKeys = Set<String>()
        allKeys.formUnion(sessionMap.keys)
        allKeys.formUnion(sessionByKey.keys)
        allKeys.formUnion(chatBySession.keys)
        allKeys.formUnion(recentSessionKeys)

        var built: [SessionEntry] = []
        for key in allKeys {
            let sid = sessionMap[key] ?? "-"
            let meta = sessionByKey[key]
            built.append(
                SessionEntry(
                    sessionKey: key,
                    sessionId: sid,
                    channel: meta?.channel ?? "-",
                    sender: meta?.sender ?? "-",
                    threadId: meta?.thread ?? "-",
                    lastText: meta?.text ?? "(no recent chat found)",
                    lastTime: meta?.ts ?? ""
                )
            )
        }

        built.sort { lhs, rhs in
            if lhs.lastTime.isEmpty && rhs.lastTime.isEmpty {
                return lhs.sessionKey < rhs.sessionKey
            }
            if lhs.lastTime.isEmpty { return false }
            if rhs.lastTime.isEmpty { return true }
            return lhs.lastTime > rhs.lastTime
        }

        sessions = built
        let valid = Set(sessions.map { $0.sessionKey })
        recentSessionKeys = recentSessionKeys.filter { valid.contains($0) }
        if let selected = selectedSessionKey, !sessions.contains(where: { $0.sessionKey == selected }) {
            selectedSessionKey = nil
        }
        if let selected = selectedSessionKey {
            chatMessages = chatBySession[selected, default: []]
        } else {
            chatMessages = []
        }
        timelineByMsgId = timelineByMsg
    }

    func selectSession(_ key: String?) {
        selectedSessionKey = key
        if let k = key {
            recentSessionKeys.removeAll { $0 == k }
            recentSessionKeys.insert(k, at: 0)
            if recentSessionKeys.count > 8 {
                recentSessionKeys = Array(recentSessionKeys.prefix(8))
            }
        }
        refreshSessions()
    }

    func deleteSelectedSession() {
        guard let key = selectedSessionKey else { return }
        deleteSession(key: key)
    }

    func deleteAllSessions() {
        guard var node = loadStateJSON() else {
            detailText = "Delete failed: cannot read state file."
            return
        }
        node["session_map"] = [String: String]()
        if saveStateJSON(node) {
            selectedSessionKey = nil
            refreshSessions()
            detailText = "Deleted all sessions."
        } else {
            detailText = "Delete failed: cannot write state file."
        }
    }

    func deleteSession(key: String) {
        guard var node = loadStateJSON() else {
            detailText = "Delete failed: cannot read state file."
            return
        }
        var map = (node["session_map"] as? [String: Any]) ?? [:]
        map.removeValue(forKey: key)
        node["session_map"] = map
        if saveStateJSON(node) {
            if selectedSessionKey == key {
                selectedSessionKey = nil
            }
            refreshSessions()
            detailText = "Deleted session: \(key)"
        } else {
            detailText = "Delete failed: cannot write state file."
        }
    }

    private func loadStateJSON() -> [String: Any]? {
        let url = URL(fileURLWithPath: cfg.stateFile)
        guard let data = try? Data(contentsOf: url) else { return nil }
        return (try? JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    private func saveStateJSON(_ node: [String: Any]) -> Bool {
        let url = URL(fileURLWithPath: cfg.stateFile)
        do {
            let dir = url.deletingLastPathComponent()
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            let data = try JSONSerialization.data(withJSONObject: node, options: [.prettyPrinted, .sortedKeys])
            try data.write(to: url)
            return true
        } catch {
            return false
        }
    }

    func start() {
        refreshHealthChecks()
        if hasHealthFailures() {
            statusText = "Blocked"
            detailText = "Cannot start: unresolved health issues."
            return
        }
        if runningPID() != nil {
            statusText = "Running"
            detailText = "Gateway is already running.\nChannel: \(selectedChannel.title)"
            return
        }
        guard FileManager.default.fileExists(atPath: envPath) else {
            statusText = "Start failed"
            detailText = "Missing .env at \(envPath).\nRun build again or create .env first."
            return
        }

        let logDir = URL(fileURLWithPath: cfg.logFile).deletingLastPathComponent().path
        do {
            try FileManager.default.createDirectory(atPath: logDir, withIntermediateDirectories: true)
            if !FileManager.default.fileExists(atPath: cfg.logFile) {
                FileManager.default.createFile(atPath: cfg.logFile, contents: nil)
            }
        } catch {
            statusText = "Start failed"
            detailText = "Cannot prepare log file: \(error.localizedDescription)"
            return
        }

        let cmd = "cd \(shellEscape(cfg.repoRoot)) && nohup env CHANNEL_TYPE=\(selectedChannel.rawValue) PYTHONPATH=src python3 -m app.main \(shellEscape(cfg.workdir)) >>\(shellEscape(cfg.logFile)) 2>&1 &"
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/zsh")
        process.arguments = ["-lc", cmd]
        do {
            try process.run()
            process.waitUntilExit()
            Thread.sleep(forTimeInterval: 0.8)
            refreshStatus()
            if statusText == "Running" {
                detailText = "Gateway started.\nChannel: \(selectedChannel.title)\nLog: \(cfg.logFile)"
            } else {
                statusText = "Start failed"
                detailText = "Process exited but lock file is not active.\nLog: \(cfg.logFile)"
            }
        } catch {
            statusText = "Start failed"
            detailText = error.localizedDescription
        }
    }

    func stop() {
        guard let pid = runningPID() else {
            statusText = "Stopped"
            detailText = "Gateway is not running."
            return
        }
        _ = kill(pid, SIGTERM)
        Thread.sleep(forTimeInterval: 0.8)
        refreshStatus()
        if statusText == "Stopped" {
            detailText = "Stopped gateway process \(pid)."
        } else {
            detailText = "Could not stop PID \(pid). Try manual kill."
        }
    }

    func openLogs() {
        let url = URL(fileURLWithPath: cfg.logFile)
        NSWorkspace.shared.open(url)
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

    @ViewBuilder
    private var bubbleContent: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(isUser ? "You" : "Assistant")
                .font(.system(size: 10, weight: .semibold))
                .foregroundStyle(.secondary)
            Text(message.text)
                .font(.system(size: 12))
                .textSelection(.enabled)
            if !message.time.isEmpty {
                Text(message.time)
                    .font(.system(size: 10))
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(10)
    }

    var body: some View {
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
                Button("Start") { controller.start() }
                    .keyboardShortcut(.return, modifiers: [])
                    .disabled(controller.statusText == "Running")
                Button("Stop") { controller.stop() }
                    .disabled(controller.statusText != "Running")
                Button("Open Logs") { controller.openLogs() }
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

                    if !controller.recentSessionKeys.isEmpty {
                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack(spacing: 8) {
                                ForEach(controller.recentSessionKeys, id: \.self) { key in
                                    let isActive = (controller.selectedSessionKey == key)
                                    Button {
                                        controller.selectSession(key)
                                    } label: {
                                        Text(key)
                                            .lineLimit(1)
                                            .font(.system(size: 10, weight: .medium))
                                            .padding(.horizontal, 10)
                                            .padding(.vertical, 5)
                                            .background(isActive ? Color.accentColor.opacity(0.20) : Color.gray.opacity(0.14), in: Capsule())
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                        }
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
                }
                .padding(14)
                .frame(minWidth: 620, idealWidth: 720)
            }
        }
        .frame(width: 1140, height: 700)
        .onAppear {
            controller.refreshHealthChecks()
            controller.refreshStatus()
            controller.refreshSessions()
            controller.autoStartOnLaunch()
        }
        .onReceive(refreshTimer) { _ in
            controller.refreshHealthChecks()
            controller.refreshStatus()
            controller.refreshSessions()
        }
        .sheet(isPresented: $showConfig) {
            ConfigView(controller: controller)
        }
        .sheet(item: $timelineMessage) { msg in
            ProcessTimelineView(message: msg, events: controller.timeline(for: msg))
        }
    }
}

@main
struct CLIAppMain: App {
    var body: some Scene {
        WindowGroup {
            if let controller = try? GatewayController() {
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
