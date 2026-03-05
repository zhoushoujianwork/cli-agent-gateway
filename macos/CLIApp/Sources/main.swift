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

    private let cfg: GatewayConfig
    private let channelDefaultsKey = "gateway.selected_channel"
    private let hiddenSessionsDefaultsPrefix = "gateway.hidden_sessions"
    private var hiddenSessionCutoffByKey: [String: String] = [:]
    private var localOverlayMessagesBySession: [String: [ChatMessage]] = [:]
    private var didAutoStartOnLaunch = false

    init() throws {
        cfg = try GatewayController.loadConfig()
        selectedChannel = GatewayController.detectEnvChannel(repoRoot: cfg.repoRoot)
        hiddenSessionCutoffByKey = loadHiddenSessionCutoffByKey()
        refreshHealthChecks()
        refreshStatus()
        refreshSessions()
    }

    private var hiddenSessionsDefaultsKey: String {
        "\(hiddenSessionsDefaultsPrefix).\(cfg.repoRoot)"
    }

    private func nowISO8601() -> String {
        ISO8601DateFormatter().string(from: Date())
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

    private func shellOutput(_ command: String, timeoutSec: TimeInterval? = nil) -> (code: Int32, output: String) {
        let proc = Process()
        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = pipe
        proc.executableURL = URL(fileURLWithPath: "/bin/zsh")
        proc.arguments = ["-lc", command]
        do {
            try proc.run()
            if let timeoutSec {
                let deadline = Date().addingTimeInterval(timeoutSec)
                while proc.isRunning && Date() < deadline {
                    Thread.sleep(forTimeInterval: 0.05)
                }
                if proc.isRunning {
                    proc.terminate()
                    let data = pipe.fileHandleForReading.readDataToEndOfFile()
                    let text = String(data: data, encoding: .utf8) ?? ""
                    return (124, (text + "\n[timeout]").trimmingCharacters(in: .whitespacesAndNewlines))
                }
            }
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

        case .installCodexACP:
            NSWorkspace.shared.open(URL(string: "https://github.com/openai/codex")!)
            detailText = "Opened Codex setup page. Install codex-acp command manually, then retry."

        case .installIMsg:
            NSWorkspace.shared.open(URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent("docs/IMESSAGE_SETUP.md"))
            detailText = "Opened iMessage setup guide. Install and configure imsg first."

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
        return gatewayPIDsByWorkdir().first
    }

    private func gatewayPIDsByWorkdir() -> [Int32] {
        let cmd = "cd \(shellEscape(cfg.repoRoot))/src && go run ./cmd/gateway-cli status 2>/dev/null || true"
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
        writeEnvValue("CHANNEL_TYPE", value: channel.rawValue)
        refreshHealthChecks()
        refreshStatus()
    }

    func refreshStatus() {
        let pids = gatewayPIDsByWorkdir()
        if let pid = runningPID() {
            let active = runningChannelType(pid: pid) ?? selectedChannel
            activeChannelText = active.title
            statusText = "Running"
            let dupText = pids.count > 1 ? "\nDetected duplicate instances: \(pids.count)" : ""
            detailText = "PID \(pid)\nChannel: \(active.title)\nLog: \(cfg.logFile)\(dupText)"
        } else {
            activeChannelText = selectedChannel.title
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

        typealias InboundMeta = (sender: String, senderName: String, text: String, channel: String, thread: String, ts: String)
        var inboundByMsgId: [String: InboundMeta] = [:]
        var sessionByKey: [String: InboundMeta] = [:]
        var sessionKeyByMsgId: [String: String] = [:]
        var sessionIdByMsgId: [String: String] = [:]
        var chatBySession: [String: [ChatMessage]] = [:]
        var timelineByMsg: [String: [ProcessEvent]] = [:]
        var sessionResetTimes: [String: [String]] = [:]
        var segmentSessionIdByKey: [String: String] = [:]
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
                    var senderName = ""
                    if let profile = record["user_profile"] as? [String: Any] {
                        senderName = (profile["sender_name"] as? String) ?? ""
                    }
                    let text = (record["text"] as? String) ?? ""
                    let ts = (record["time"] as? String) ?? ""
                    let channel = channelFromProfile(record["user_profile"])
                    let thread = threadFromProfile(record["user_profile"])
                    inboundByMsgId[msgId] = (
                        sender: sender,
                        senderName: senderName.trimmingCharacters(in: .whitespacesAndNewlines),
                        text: text,
                        channel: channel,
                        thread: thread,
                        ts: ts
                    )
                    let computedKey = buildSessionKey(channel: channel, sender: sender, threadId: thread)
                    sessionKeyByMsgId[msgId] = computedKey
                    if let prev = sessionByKey[computedKey] {
                        if ts >= prev.ts {
                            sessionByKey[computedKey] = (
                                sender: sender,
                                senderName: senderName.trimmingCharacters(in: .whitespacesAndNewlines),
                                text: text,
                                channel: channel,
                                thread: thread,
                                ts: ts
                            )
                        }
                    } else {
                        sessionByKey[computedKey] = (
                            sender: sender,
                            senderName: senderName.trimmingCharacters(in: .whitespacesAndNewlines),
                            text: text,
                            channel: channel,
                            thread: thread,
                            ts: ts
                        )
                    }
                }

                if kind == "trace",
                   let stage = record["stage"] as? String,
                   stage == "session_resolved",
                   let sessionKey = record["session_key"] as? String,
                   let msgId = record["msg_id"] as? String,
                   let inbound = inboundByMsgId[msgId] {
                    sessionKeyByMsgId[msgId] = sessionKey
                    if let sid = record["session_id"] as? String {
                        let v = sid.trimmingCharacters(in: .whitespacesAndNewlines)
                        if !v.isEmpty {
                            sessionIdByMsgId[msgId] = v
                        }
                    }
                    if let prev = sessionByKey[sessionKey] {
                        if inbound.ts >= prev.ts {
                            sessionByKey[sessionKey] = inbound
                        }
                    } else {
                        sessionByKey[sessionKey] = inbound
                    }
                }

                if kind == "trace",
                   let msgId = record["msg_id"] as? String,
                   let sid = record["session_id"] as? String {
                    let v = sid.trimmingCharacters(in: .whitespacesAndNewlines)
                    if !v.isEmpty {
                        sessionIdByMsgId[msgId] = v
                    }
                }

                if kind == "session_command",
                   let sessionKey = record["session_key"] as? String,
                   let command = record["command"] as? String,
                   let ts = record["time"] as? String,
                   command == "/clear" || command == "/new" {
                    sessionResetTimes[sessionKey, default: []].append(ts)
                }
            }

            for key in sessionResetTimes.keys {
                let sortedUnique = Array(Set(sessionResetTimes[key] ?? [])).sorted()
                sessionResetTimes[key] = sortedUnique
            }

            func segmentedSessionKey(base: String, ts: String) -> String {
                guard !base.isEmpty else { return base }
                let cuts = sessionResetTimes[base] ?? []
                if cuts.isEmpty {
                    return base
                }
                let idx = cuts.filter { ts >= $0 }.count
                if idx >= cuts.count {
                    return base
                }
                return "\(base)#\(idx)"
            }

            var remappedSessionKeyByMsgId: [String: String] = [:]
            var remappedSessionByKey: [String: InboundMeta] = [:]
            for (msgId, baseKey) in sessionKeyByMsgId {
                let ts = inboundByMsgId[msgId]?.ts ?? ""
                let segKey = segmentedSessionKey(base: baseKey, ts: ts)
                remappedSessionKeyByMsgId[msgId] = segKey
                if let inbound = inboundByMsgId[msgId] {
                    if let prev = remappedSessionByKey[segKey] {
                        if inbound.ts >= prev.ts {
                            remappedSessionByKey[segKey] = inbound
                        }
                    } else {
                        remappedSessionByKey[segKey] = inbound
                    }
                }
            }
            sessionKeyByMsgId = remappedSessionKeyByMsgId
            sessionByKey = remappedSessionByKey

            var segmentSessionIdTs: [String: String] = [:]
            for (msgId, segKey) in sessionKeyByMsgId {
                guard let sid = sessionIdByMsgId[msgId], !sid.isEmpty else { continue }
                let ts = inboundByMsgId[msgId]?.ts ?? ""
                if let prevTs = segmentSessionIdTs[segKey], !prevTs.isEmpty, !ts.isEmpty, ts < prevTs {
                    continue
                }
                segmentSessionIdTs[segKey] = ts
                segmentSessionIdByKey[segKey] = sid
            }

            for record in records {
                guard let kind = record["kind"] as? String else { continue }

                if kind == "inbound_received",
                   let msgId = record["msg_id"] as? String,
                   let sessionKey = sessionKeyByMsgId[msgId] {
                    let text = ((record["text"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !text.isEmpty {
                        let ts = (record["time"] as? String) ?? ""
                        let msg = ChatMessage(
                            id: "\(msgId)-u",
                            sourceMsgId: msgId,
                            role: "user",
                            text: text,
                            time: ts,
                            deliveryStatus: .sent,
                            statusDetail: ""
                        )
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
                        let msg = ChatMessage(
                            id: "\(msgId)-a",
                            sourceMsgId: msgId,
                            role: "assistant",
                            text: text,
                            time: ts,
                            deliveryStatus: .sent,
                            statusDetail: ""
                        )
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
                        let msg = ChatMessage(
                            id: "\(msgId)-e",
                            sourceMsgId: msgId,
                            role: "assistant",
                            text: "Error: \(text)",
                            time: ts,
                            deliveryStatus: .failed,
                            statusDetail: text
                        )
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

        func displaySessionId(for sessionKey: String) -> String {
            if let sid = segmentSessionIdByKey[sessionKey], !sid.isEmpty {
                return sid
            }
            let base = sessionKey.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? sessionKey
            if let sid = sessionMap[base], !sid.isEmpty {
                return sid
            }
            if let suffix = sessionKey.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false).dropFirst().first {
                return "segment-\(suffix)"
            }
            return "-"
        }

        var built: [SessionEntry] = []
        for key in allKeys {
                    let sid = displaySessionId(for: key)
                    let meta = sessionByKey[key]
                    let shownSender = {
                        let name = meta?.senderName ?? ""
                        if !name.isEmpty { return name }
                        return meta?.sender ?? "-"
                    }()
                    built.append(
                        SessionEntry(
                            sessionKey: key,
                    sessionId: sid,
                    channel: meta?.channel ?? "-",
                    senderId: meta?.sender ?? "-",
                    sender: shownSender,
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

        let hiddenCountBefore = hiddenSessionCutoffByKey.count
        built = built.filter { session in
            shouldShowSession(session)
        }
        if hiddenSessionCutoffByKey.count != hiddenCountBefore {
            saveHiddenSessionCutoffByKey()
        }
        sessions = built
        if let selected = selectedSessionKey, !sessions.contains(where: { $0.sessionKey == selected }) {
            selectedSessionKey = nil
        }
        if let selected = selectedSessionKey {
            chatMessages = mergedMessages(for: selected, persisted: chatBySession[selected, default: []])
        } else {
            chatMessages = []
        }
        timelineByMsgId = timelineByMsg
    }

    func selectSession(_ key: String?) {
        selectedSessionKey = key
        refreshSessions()
    }

    private func selectedSessionEntry() -> SessionEntry? {
        guard let key = selectedSessionKey else { return nil }
        return sessions.first(where: { $0.sessionKey == key })
    }

    private func mergedMessages(for sessionKey: String, persisted: [ChatMessage]) -> [ChatMessage] {
        let overlay = localOverlayMessagesBySession[sessionKey, default: []]
        var merged = persisted
        merged.append(contentsOf: overlay)
        return merged.sorted { $0.time < $1.time }
    }

    private func appendOverlayMessage(_ msg: ChatMessage, sessionKey: String) {
        localOverlayMessagesBySession[sessionKey, default: []].append(msg)
        if selectedSessionKey == sessionKey {
            chatMessages.append(msg)
        }
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
        for raw in text.split(separator: "\n").reversed() {
            let line = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            if line.hasPrefix("{"), line.hasSuffix("}") {
                return line
            }
        }
        return nil
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
        guard var node = loadStateJSON() else { return false }
        var map = (node["session_map"] as? [String: Any]) ?? [:]
        map.removeValue(forKey: baseSessionKey)
        node["session_map"] = map
        return saveStateJSON(node)
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
        guard let session = selectedSessionEntry() else {
            detailText = "Select a session first."
            return
        }
        let selectedSessionKey = session.sessionKey
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
                refreshSessions()
                return
            }
            appendLocalActionMessage(
                cleared ? "Action /new: session reset." : "Action /new warning: reset failed, sending anyway.",
                sessionKey: selectedSessionKey
            )
            detailText = cleared ? "New session started." : "Could not reset old session; continuing send."
            if cmd.payload.isEmpty {
                localDraftText = ""
                refreshSessions()
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
        localSending = false
        detailText = "Local chat TODO: Go-native path not implemented yet."
        updateOverlayMessage(
            sessionKey: selectedSessionKey,
            messageId: userMsgId,
            deliveryStatus: .failed,
            statusDetail: "TODO: Go-native local chat not implemented"
        )
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
            for s in sessions {
                hideSessionKey(s.sessionKey)
            }
            saveHiddenSessionCutoffByKey()
            selectedSessionKey = nil
            refreshSessions()
            detailText = "Deleted all sessions."
        } else {
            detailText = "Delete failed: cannot write state file."
        }
    }

    func deleteSession(key: String) {
        if key.contains("#") {
            hideSessionKey(key)
            saveHiddenSessionCutoffByKey()
            if selectedSessionKey == key {
                selectedSessionKey = nil
            }
            refreshSessions()
            detailText = "Deleted archived session segment from app list."
            return
        }
        guard var node = loadStateJSON() else {
            detailText = "Delete failed: cannot read state file."
            return
        }
        var map = (node["session_map"] as? [String: Any]) ?? [:]
        map.removeValue(forKey: key)
        node["session_map"] = map
        if saveStateJSON(node) {
            for session in sessions {
                if session.sessionKey == key || session.sessionKey.hasPrefix("\(key)#") {
                    hideSessionKey(session.sessionKey)
                }
            }
            saveHiddenSessionCutoffByKey()
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
        let existing = gatewayPIDsByWorkdir()
        if !existing.isEmpty {
            statusText = "Running"
            detailText = "Gateway is already running.\nPIDs: \(existing.map(String.init).joined(separator: ", "))\nChannel: \(selectedChannel.title)"
            return
        }
        guard FileManager.default.fileExists(atPath: envPath) else {
            statusText = "Start failed"
            detailText = "Missing .env at \(envPath).\nRun build again or create .env first."
            return
        }
        writeEnvValue("CHANNEL_TYPE", value: selectedChannel.rawValue)

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

        let cmd = "cd \(shellEscape(cfg.repoRoot))/src && nohup env CHANNEL_TYPE=\(selectedChannel.rawValue) LOCK_FILE=\(shellEscape(cfg.lockFile)) STATE_FILE=\(shellEscape(cfg.stateFile)) INTERACTION_LOG_FILE=\(shellEscape(cfg.interactionLogFile)) go run ./cmd/gateway-cli run >>\(shellEscape(cfg.logFile)) 2>&1 &"
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
        let pids = gatewayPIDsByWorkdir()
        guard !pids.isEmpty else {
            statusText = "Stopped"
            detailText = "Gateway is not running."
            return
        }
        for pid in pids {
            _ = kill(pid, SIGTERM)
        }
        Thread.sleep(forTimeInterval: 0.8)
        refreshStatus()
        if statusText == "Stopped" {
            detailText = "Stopped gateway process(es): \(pids.map(String.init).joined(separator: ", "))."
        } else {
            detailText = "Could not stop all gateway processes.\nExpected stopped: \(pids.map(String.init).joined(separator: ", "))."
        }
    }

    func restart() {
        statusText = "Restarting"
        detailText = "Restarting gateway..."
        if runningPID() != nil {
            stop()
            if runningPID() != nil {
                detailText = "Restart failed: gateway is still running."
                return
            }
        }
        start()
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
            controller.refreshHealthChecks()
            controller.refreshStatus()
            controller.refreshSessions()
            controller.autoStartOnLaunch()
        }
        .onReceive(refreshTimer) { _ in
            refreshTick += 1
            controller.refreshStatus()
            if !controller.localSending && (refreshTick % 3 == 0) {
                controller.refreshSessions()
            }
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
