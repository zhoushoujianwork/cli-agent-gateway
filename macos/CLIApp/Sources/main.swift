import AppKit
import CryptoKit
import Foundation
import SwiftUI

extension Notification.Name {
    static let guiOpenSettings = Notification.Name("gui.openSettings")
    static let guiToggleLogDrawer = Notification.Name("gui.toggleLogDrawer")
    static let guiOpenCurrentLog = Notification.Name("gui.openCurrentLog")
}

enum ChannelType: String, CaseIterable, Identifiable {
    case command
    case imessage
    case dingtalk

    var id: String { rawValue }

    var title: String {
        switch self {
        case .command:
            return "GUI Chat"
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
    case installCAG
}

struct ComponentStatus: Identifiable {
    let id: String
    let name: String
    let installed: Bool
    let installCommand: String
    let description: String
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
    let channel: String
    let senderId: String
    let sender: String
    let threadId: String
    let lastText: String
    let lastTime: String
    let workdir: String
    let latest: Bool

    var id: String { sessionKey }
}

struct AccessUserEntry: Identifiable {
    let key: String
    let channel: String
    let userID: String
    let senderName: String
    let status: String
    let firstSeenAt: String
    let lastSeenAt: String
    let lastMessageID: String
    let lastText: String
    let threadID: String
    let conversationTitle: String

    var id: String { key }
    var displayName: String { senderName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? userID : senderName }
}

enum MessageDeliveryStatus: String {
    case sending
    case sent
    case processing
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

enum LocalTimeDisplay {
    private static let parseWithFractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let parseBasic: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    private static let outputFormatter: DateFormatter = {
        let f = DateFormatter()
        f.locale = .current
        f.timeZone = .current
        f.dateStyle = .medium
        f.timeStyle = .medium
        return f
    }()

    static func text(_ raw: String) -> String {
        let value = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else { return "" }
        let date = parseWithFractional.date(from: value) ?? parseBasic.date(from: value)
        guard let date else { return value }
        return outputFormatter.string(from: date)
    }
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

final class LogTailController: ObservableObject {
    @Published var content: String = ""
    @Published var logPath: String = ""
    @Published var isLoading: Bool = false
    @Published var autoRefresh: Bool = true
    @Published var followTail: Bool = true
    @Published var lineCount: Int = 240
    @Published var lastUpdatedText: String = ""

    private let pathsProvider: () -> [String]
    private var refreshTimer: Timer?

    init(pathsProvider: @escaping () -> [String]) {
        self.pathsProvider = pathsProvider
    }

    deinit {
        stop()
    }

    func start() {
        refresh()
        guard refreshTimer == nil else { return }
        refreshTimer = Timer.scheduledTimer(withTimeInterval: 1.5, repeats: true) { [weak self] _ in
            guard let self, self.autoRefresh else { return }
            self.refresh()
        }
    }

    func stop() {
        refreshTimer?.invalidate()
        refreshTimer = nil
    }

    func refresh() {
        let targets = pathsProvider()
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        logPath = targets.joined(separator: "\n")
        isLoading = true
        DispatchQueue.global(qos: .utility).async { [weak self] in
            guard let self else { return }
            let rendered: String
            if targets.isEmpty {
                rendered = "Log tail unavailable: empty log path."
            } else {
                var sections: [String] = []
                for target in targets {
                    let title = URL(fileURLWithPath: target).lastPathComponent
                    if !FileManager.default.fileExists(atPath: target) {
                        sections.append("=== \(title) ===\nLog file not found.\n\(target)")
                        continue
                    }
                    if let content = try? String(contentsOfFile: target, encoding: .utf8) {
                        let recent = content.split(separator: "\n", omittingEmptySubsequences: false).suffix(max(1, self.lineCount))
                        sections.append("=== \(title) ===\n" + recent.joined(separator: "\n"))
                    } else {
                        sections.append("=== \(title) ===\nFailed to read log file.\n\(target)")
                    }
                }
                rendered = sections.joined(separator: "\n\n")
            }
            DispatchQueue.main.async {
                self.content = rendered
                self.isLoading = false
                self.lastUpdatedText = LocalTimeDisplay.text(ISO8601DateFormatter().string(from: Date()))
            }
        }
    }
}

final class GatewayController: ObservableObject {
    typealias CAGJSONResult = (code: Int32, json: [String: Any]?, raw: String)

    @Published var statusText: String = "Checking status..."
    @Published var activeChannelText: String = "Unknown"
    @Published var detailText: String = ""
    @Published var selectedChannel: ChannelType
    @Published var sessions: [SessionEntry] = []
    @Published var selectedSessionKey: String?
    @Published var chatMessages: [ChatMessage] = []
    @Published var healthChecks: [HealthCheckItem] = []
    @Published var accessUsers: [AccessUserEntry] = []
    @Published var timelineByMsgId: [String: [ProcessEvent]] = [:]
    @Published var localDraftText: String = ""
    @Published var localSending: Bool = false
    @Published var componentChecks: [ComponentStatus] = []
    @Published var currentLogFile: String = ""
    @Published var gatewayAddressText: String = ""
    @Published var selectedSessionWorkdir: String = ""
    @Published var envFilePath: String = ""
    @Published var globalEnvFilePath: String = ""
    @Published var configReloadVersion: Int = 0
    @Published var needsInitialSetup: Bool = false
    @Published var initialSetupBusy: Bool = false
    @Published var initialSetupMessage: String = ""

    private let cfg: GatewayConfig
    private let channelDefaultsKey = "gateway.selected_channel"
    private let sessionWorkdirDefaultsPrefix = "gateway.session_workdir"
    private var sessionWorkdirByKey: [String: String] = [:]
    private var localOverlayMessagesBySession: [String: [ChatMessage]] = [:]
    private var lastLocalSendFingerprint: String = ""
    private var lastLocalSendAt: Date = .distantPast
    private var didAutoStartOnLaunch = false
    private let refreshLock = NSLock()
    private var refreshingHealth = false
    private var refreshingStatus = false
    private var refreshingSessions = false
    private var refreshingChat = false
    private var refreshingUsers = false
    private var configValuesByKey: [String: String] = [:]

    init() throws {
        cfg = try GatewayController.loadConfig()
        let detectedChannel = GatewayController.detectEnvChannel(repoRoot: cfg.repoRoot)
        selectedChannel = GatewayController.loadSavedChannel(defaultChannel: detectedChannel)
        sessionWorkdirByKey = loadSessionWorkdirByKey()
        currentLogFile = cfg.logFile
        envFilePath = URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent(".env").path
        globalEnvFilePath = GatewayController.userEnvPath()
        selectedSessionWorkdir = ""
        componentChecks = checkRequiredComponents()
        let guiLogPath = URL(fileURLWithPath: cfg.logFile).deletingLastPathComponent().appendingPathComponent("gui.log").path
        GUILogger.shared.setLogPath(guiLogPath)
        log("controller init repo=\(cfg.repoRoot) workdir=\(cfg.workdir)")
    }

    private var sessionWorkdirDefaultsKey: String {
        "\(sessionWorkdirDefaultsPrefix).\(cfg.repoRoot)"
    }

    private func log(_ message: String) {
        GUILogger.shared.log(message)
    }

    private func loadSessionWorkdirByKey() -> [String: String] {
        guard let raw = UserDefaults.standard.dictionary(forKey: sessionWorkdirDefaultsKey) else {
            return [:]
        }
        var out: [String: String] = [:]
        for (k, v) in raw {
            guard let path = v as? String else { continue }
            let key = k.trimmingCharacters(in: .whitespacesAndNewlines)
            let cleanedPath = path.trimmingCharacters(in: .whitespacesAndNewlines)
            if key.isEmpty || cleanedPath.isEmpty { continue }
            out[key] = cleanedPath
        }
        return out
    }

    private func saveSessionWorkdirByKey() {
        UserDefaults.standard.set(sessionWorkdirByKey, forKey: sessionWorkdirDefaultsKey)
    }

    private func baseSessionKey(_ key: String) -> String {
        key.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? key
    }

    private func workdirForSessionKey(_ key: String) -> String {
        let base = baseSessionKey(key)
        if let sessionWorkdir = sessions.first(where: { $0.sessionKey == key || baseSessionKey($0.sessionKey) == base })?.workdir
            .trimmingCharacters(in: .whitespacesAndNewlines),
           !sessionWorkdir.isEmpty {
            return sessionWorkdir
        }
        return (sessionWorkdirByKey[base] ?? sessionWorkdirByKey[key] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func refreshSelectedSessionWorkdir() {
        guard let key = selectedSessionKey, !key.isEmpty else {
            selectedSessionWorkdir = ""
            return
        }
        selectedSessionWorkdir = workdirForSessionKey(key)
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
        let srcPath = URL(fileURLWithPath: repoRoot).appendingPathComponent("src").path
        let escapedSrcPath = "'" + srcPath.replacingOccurrences(of: "'", with: "'\\''") + "'"
        let cmd = "cd \(escapedSrcPath) && go run ./cmd/gateway-cli config get CHANNEL_TYPE"
        let proc = Process()
        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe
        proc.executableURL = URL(fileURLWithPath: "/bin/zsh")
        proc.arguments = ["-lc", cmd]
        do {
            try proc.run()
            proc.waitUntilExit()
        } catch {
            return .command
        }
        let output = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        let line = output.trimmingCharacters(in: .whitespacesAndNewlines)
        guard line.hasPrefix("CHANNEL_TYPE=") else { return .command }
        let rest = String(line.dropFirst("CHANNEL_TYPE=".count))
        let value = rest.split(separator: "\t", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? rest
        return ChannelType(rawValue: value) ?? .command
    }

    private static func userEnvPath() -> String {
        URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent(".cag/.env").path
    }

    private static func loadSavedChannel(defaultChannel: ChannelType) -> ChannelType {
        guard let raw = UserDefaults.standard.string(forKey: "gateway.selected_channel") else {
            return defaultChannel
        }
        return ChannelType(rawValue: raw) ?? defaultChannel
    }

    private var envPath: String { URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent(".env").path }
    private var userEnvPath: String { Self.userEnvPath() }

    var repoRootPath: String { cfg.repoRoot }

    var gatewayWorkdirPath: String { cfg.workdir }

    var effectiveLogPath: String { currentLogFile.isEmpty ? cfg.logFile : currentLogFile }

    var liveLogPaths: [String] {
        let primary = effectiveLogPath.trimmingCharacters(in: .whitespacesAndNewlines)
        return primary.isEmpty ? [] : [primary]
    }

    private func envValue(_ key: String) -> String? {
        if let value = configValuesByKey[key] {
            return value
        }
        let loaded = loadConfigValuesSnapshot()
        configValuesByKey = loaded
        return loaded[key]
    }

    func envValueOrDefault(_ key: String, fallback: String) -> String {
        let value = (envValue(key) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        return value.isEmpty ? fallback : value
    }

    private func writeConfigValue(_ key: String, value: String, global: Bool) {
        var args = ["set", key, value]
        if global {
            args.append("--global")
        }
        _ = cagCommand("config", args: args, timeoutSec: 8)
    }

    func writeEnvValue(_ key: String, value: String) {
        writeConfigValue(key, value: value, global: false)
        refreshConfigPanel()
    }

    func writeUserEnvValue(_ key: String, value: String) {
        writeConfigValue(key, value: value, global: true)
        refreshConfigPanel()
    }

    func writeEnvValues(_ values: [(String, String)]) {
        for (key, value) in values {
            writeConfigValue(key, value: value, global: false)
        }
        refreshConfigPanel()
    }

    func writeUserEnvValues(_ values: [(String, String)]) {
        for (key, value) in values {
            writeConfigValue(key, value: value, global: true)
        }
        refreshConfigPanel()
    }

    func refreshConfigPanel() {
        envFilePath = envPath
        globalEnvFilePath = userEnvPath
        refreshConfigSnapshotAsync()
        refreshHealthChecksAsync()
        refreshStatusAsync()
        refreshAccessUsersAsync()
    }

    private func refreshConfigSnapshotAsync() {
        runInBackground { [weak self] in
            guard let self else { return }
            let snapshot = self.loadConfigValuesSnapshot()
            self.onMain {
                self.configValuesByKey = snapshot
                self.configReloadVersion += 1
            }
        }
    }

    private func loadConfigValuesSnapshot() -> [String: String] {
        let res = cagCommand("config", args: ["show"], timeoutSec: 8)
        guard res.code == 0 else { return [:] }
        return parseConfigValues(res.output)
    }

    private func parseConfigValues(_ raw: String) -> [String: String] {
        var values: [String: String] = [:]
        for line in raw.split(separator: "\n", omittingEmptySubsequences: false) {
            let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmed.isEmpty else { continue }
            let entry = trimmed.split(separator: "\t", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? trimmed
            guard let eq = entry.firstIndex(of: "=") else { continue }
            let key = String(entry[..<eq]).trimmingCharacters(in: .whitespacesAndNewlines)
            let valueStart = entry.index(after: eq)
            let value = String(entry[valueStart...]).trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
            guard !key.isEmpty else { continue }
            values[key] = value
        }
        return values
    }

    private func requiresInitialSetup() -> Bool {
        !FileManager.default.fileExists(atPath: envPath)
    }

    func pickConfigWorkdir(currentValue: String, completion: @escaping (String) -> Void) {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = true
        panel.prompt = "Use Workdir"
        panel.message = "Select the local working directory used by GUI chat."
        let fallback = currentValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? cfg.repoRoot : currentValue
        panel.directoryURL = URL(fileURLWithPath: fallback)
        let response = panel.runModal()
        guard response == .OK, let dirURL = panel.url else { return }
        completion(dirURL.path)
    }

    private func cagCommand(_ action: String, args: [String] = [], timeoutSec: TimeInterval? = nil, direct: Bool = false) -> (code: Int32, output: String) {
        let runner = cagRunner()
        let cmdParts = runner.prefix + [action] + args
        let full = cmdParts.map { shellEscape($0) }.joined(separator: " ")
        let envPrefix = direct ? "CAG_GRPC_DISABLE=1 " : ""
        let cmd = "cd \(shellEscape(runner.workdir)) && \(envPrefix)\(full)"
        let out = shellOutput(cmd, timeoutSec: timeoutSec)
        return (out.code, out.output)
    }

    func testActiveChannelAsync(completion: @escaping (Bool, String) -> Void) {
        runInBackground { [weak self] in
            guard let self else { return }
            let res = self.cagJSON("doctor", timeoutSec: 10, direct: true)
            var ok = false
            var message = "Channel test failed."
            if let node = res.json {
                ok = (node["ok"] as? Bool) ?? false
                if let items = node["items"] as? [[String: Any]] {
                    let failed = items.compactMap { item -> String? in
                        let passed = (item["ok"] as? Bool) ?? false
                        if passed { return nil }
                        return (item["detail"] as? String) ?? (item["key"] as? String) ?? "check failed"
                    }
                    if failed.isEmpty {
                        message = "Active channel looks ready."
                    } else {
                        message = failed.prefix(3).joined(separator: "\n")
                    }
                } else {
                    message = ok ? "Active channel looks ready." : (res.raw.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? message : res.raw.trimmingCharacters(in: .whitespacesAndNewlines))
                }
            } else if !res.raw.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                message = res.raw.trimmingCharacters(in: .whitespacesAndNewlines)
            }
            self.onMain {
                completion(ok, message)
            }
        }
    }

    func completeInitialSetup() {
        initialSetupBusy = true
        initialSetupMessage = "Initializing gateway config..."
        runInBackground { [weak self] in
            guard let self else { return }
            _ = self.cagJSON("gatewayd-down", timeoutSec: 3)
            let configRes = self.cagCommand("config", timeoutSec: 20)
            self.onMain {
                guard configRes.code == 0 else {
                    self.initialSetupBusy = false
                    self.initialSetupMessage = "Config init failed.\n\(configRes.output.trimmingCharacters(in: .whitespacesAndNewlines))"
                    return
                }
                self.selectedChannel = .command
                UserDefaults.standard.set(ChannelType.command.rawValue, forKey: self.channelDefaultsKey)
                self.envFilePath = self.envPath
                self.initialSetupBusy = false
                self.initialSetupMessage = "Initialization complete. Gateway session workdir can be set per workspace session."
                self.needsInitialSetup = false
                self.refreshConfigPanel()
                self.refreshSessionsAsync()
            }
        }
    }

    func addSessionByPickingWorkdir() {
        let fallbackDir = cfg.workdir.isEmpty ? cfg.repoRoot : cfg.workdir
        guard let selectedPath = pickSessionWorkdir(
            fallbackDir: fallbackDir,
            prompt: "Create Gateway Session",
            message: "Select a local working directory for the new gateway session."
        ) else {
            detailText = "Create Session cancelled."
            return
        }
        let sessionKey = generatedSessionKey(for: selectedPath)
        upsertSessionWorkdir(sessionKey: sessionKey, workdir: selectedPath) { [weak self] ok, message in
            guard let self else { return }
            if ok {
                self.selectedSessionKey = sessionKey
                self.refreshSelectedSessionWorkdir()
                self.chatMessages = []
                self.timelineByMsgId = [:]
                self.refreshSessionsAsync()
                self.detailText = "Created gateway session: \(sessionKey)"
                self.log("session created session_key=\(sessionKey) path=\(selectedPath)")
            } else {
                self.detailText = "Create session failed: \(message)"
            }
        }
    }

    func updateSelectedSessionWorkdir() {
        guard let key = selectedSessionKey, !key.isEmpty else {
            detailText = "Select a gateway session first."
            return
        }
        let targetSessionKey = baseSessionKey(key)
        let fallbackDir = workdirForSessionKey(targetSessionKey).isEmpty
            ? (cfg.workdir.isEmpty ? cfg.repoRoot : cfg.workdir)
            : workdirForSessionKey(targetSessionKey)
        guard let selectedPath = pickSessionWorkdir(
            fallbackDir: fallbackDir,
            prompt: "Use Workdir",
            message: "Select a local working directory for this gateway session's CLI runs."
        ) else {
            detailText = "Update workdir cancelled."
            return
        }
        upsertSessionWorkdir(sessionKey: targetSessionKey, workdir: selectedPath) { [weak self] ok, message in
            guard let self else { return }
            if ok {
                self.refreshSelectedSessionWorkdir()
                self.refreshSessionsAsync()
                self.detailText = "Gateway session workdir updated: \(selectedPath)"
                self.log("session workdir updated session_key=\(targetSessionKey) path=\(selectedPath)")
            } else {
                self.detailText = "Update workdir failed: \(message)"
            }
        }
    }

    private func pickSessionWorkdir(fallbackDir: String, prompt: String, message: String) -> String? {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = true
        panel.prompt = prompt
        panel.message = message
        panel.directoryURL = URL(fileURLWithPath: fallbackDir)

        let response = panel.runModal()
        guard response == .OK, let dirURL = panel.url else { return nil }

        let selectedPath = dirURL.path.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !selectedPath.isEmpty else { return nil }
        var isDir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: selectedPath, isDirectory: &isDir), isDir.boolValue else { return nil }
        return selectedPath
    }

    private func upsertSessionWorkdir(sessionKey: String, workdir: String, completion: @escaping (Bool, String) -> Void) {
        let targetSessionKey = baseSessionKey(sessionKey)
        cagJSONAsync("session-new", args: ["--session-key", targetSessionKey, "--workdir", workdir]) { [weak self] result in
            guard let self else { return }
            let ok = (result.json?["ok"] as? Bool) ?? false
            if ok {
                self.syncCachedSessionWorkdir(targetSessionKey, workdir: workdir)
                completion(true, "")
                return
            }
            completion(false, self.actionErrorMessage(from: result) ?? "command failed")
        }
    }

    private func actionErrorMessage(from result: CAGJSONResult) -> String? {
        if let errorNode = result.json?["error"] as? [String: Any] {
            let message = ((errorNode["message"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            if !message.isEmpty {
                return message
            }
        }
        let raw = result.raw.trimmingCharacters(in: .whitespacesAndNewlines)
        return raw.isEmpty ? nil : raw
    }

    private func generatedSessionKey(for workdir: String) -> String {
        let baseName = URL(fileURLWithPath: workdir).lastPathComponent.trimmingCharacters(in: .whitespacesAndNewlines)
        let lowered = (baseName.isEmpty ? "session" : baseName).lowercased()
        let slugScalars = lowered.unicodeScalars.map { scalar -> Character in
            CharacterSet.alphanumerics.contains(scalar) ? Character(String(scalar)) : "-"
        }
        let slug = String(slugScalars)
            .replacingOccurrences(of: "--", with: "-")
            .trimmingCharacters(in: CharacterSet(charactersIn: "-"))
        let safeSlug = slug.isEmpty ? "session" : slug
        let millis = Int(Date().timeIntervalSince1970 * 1000)
        return "gui-\(safeSlug)-\(millis)"
    }

    func openCurrentLog() {
        let path = effectiveLogPath.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !path.isEmpty else {
            detailText = "Log path unavailable."
            return
        }
        let fileURL = URL(fileURLWithPath: path)
        if FileManager.default.fileExists(atPath: path) {
            _ = NSWorkspace.shared.open(fileURL)
            detailText = "Opened log: \(path)"
            log("log open path=\(path) exists=true")
            return
        }
        let dirURL = fileURL.deletingLastPathComponent()
        if FileManager.default.fileExists(atPath: dirURL.path) {
            _ = NSWorkspace.shared.open(dirURL)
            detailText = "Log file not created yet. Opened log folder."
            log("log open path=\(path) exists=false opened_dir=\(dirURL.path)")
            return
        }
        detailText = "Log path not found: \(path)"
        log("log open path=\(path) exists=false opened_dir=false")
    }

    private func syncCachedSessionWorkdir(_ key: String, workdir: String) {
        let targetSessionKey = baseSessionKey(key)
        sessionWorkdirByKey[targetSessionKey] = workdir
        saveSessionWorkdirByKey()
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

    private func newestSourceModDate() -> Date? {
        let srcRoot = URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent("src").path
        var latest: Date?
        if let enumerator = FileManager.default.enumerator(atPath: srcRoot) {
            for case let rel as String in enumerator {
                guard rel.hasSuffix(".go") else { continue }
                let fullPath = URL(fileURLWithPath: srcRoot).appendingPathComponent(rel).path
                guard let mod = fileModDate(fullPath) else { continue }
                if latest == nil || mod > latest! {
                    latest = mod
                }
            }
        }
        for extra in ["go.mod", "go.sum"] {
            let path = URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent(extra).path
            guard let mod = fileModDate(path) else { continue }
            if latest == nil || mod > latest! {
                latest = mod
            }
        }
        return latest
    }

    private func cagRunner() -> (workdir: String, prefix: [String]) {
        let binPath = URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent("bin/cag").path
        if FileManager.default.isExecutableFile(atPath: binPath) {
            if let binMod = fileModDate(binPath), let srcMod = newestSourceModDate(), binMod >= srcMod {
                return (cfg.repoRoot, [binPath])
            }
            log("runner fallback reason=stale_bin bin=\(binPath)")
        }
        let srcPath = URL(fileURLWithPath: cfg.repoRoot).appendingPathComponent("src").path
        return (srcPath, ["go", "run", "./cmd/gateway-cli"])
    }

    private func cagJSON(_ action: String, args: [String] = [], timeoutSec: TimeInterval? = nil, direct: Bool = false) -> CAGJSONResult {
        let t0 = Date()
        let runner = cagRunner()
        let cmdParts = runner.prefix + [action] + args + ["--json"]
        let full = cmdParts.map { shellEscape($0) }.joined(separator: " ")
        let envPrefix = direct ? "CAG_GRPC_DISABLE=1 " : ""
        let cmd = "cd \(shellEscape(runner.workdir)) && \(envPrefix)\(full)"
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

    private func cagJSONAsync(_ action: String, args: [String] = [], timeoutSec: TimeInterval? = nil, direct: Bool = false, completion: @escaping (CAGJSONResult) -> Void) {
        runInBackground { [weak self] in
            guard let self else { return }
            let result = self.cagJSON(action, args: args, timeoutSec: timeoutSec, direct: direct)
            self.onMain {
                completion(result)
            }
        }
    }

    private func repairActionForCheck(_ key: String) -> RepairAction? {
        if key == "env" || key == "config" {
            return .setupEnv
        }
        if key == "acp" {
            return .installCodexACP
        }
        if key == "cag" {
            return .installCAG
        }
        if key.hasPrefix("imessage") {
            return .installIMsg
        }
        return nil
    }

    func checkRequiredComponents() -> [ComponentStatus] {
        let cagInstalled = commandExists("cag")
        let codexAcpInstalled = commandExists("codex-acp")

        return [
            ComponentStatus(
                id: "cag",
                name: "cag CLI",
                installed: cagInstalled,
                installCommand: "brew install cag 或者 go install ./cmd/gateway-cli",
                description: "CLI Agent Gateway 命令行工具"
            ),
            ComponentStatus(
                id: "codex-acp",
                name: "codex-acp",
                installed: codexAcpInstalled,
                installCommand: "brew install codex-acp 或者 npm install -g @anthropic-ai/codex-acp",
                description: " Anthropic Codex Agent"
            )
        ]
    }

    func installCAG() {
        let script = """
        tell application "Terminal"
            activate
            do script "cd \(shellEscape(cfg.repoRoot)) && make build && ln -sf \\(pwd)/bin/cag /usr/local/bin/cag"
        end tell
        """
        _ = shellOutput("osascript -e \"\(script.replacingOccurrences(of: "\"", with: "\\\""))\"")
        detailText = "正在构建并安装 cag CLI..."
    }

    func installCodexACP() {
        NSWorkspace.shared.open(URL(string: "https://github.com/anthropics/codex-cli")!)
        detailText = "请安装 codex-acp 后重试。"
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
        case "chat":
            if refreshingChat { return false }
            refreshingChat = true
        case "users":
            if refreshingUsers { return false }
            refreshingUsers = true
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
        case "chat":
            refreshingChat = false
        case "users":
            refreshingUsers = false
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

    func refreshComponentChecksAsync() {
        runInBackground { [weak self] in
            guard let self else { return }
            let checks = self.checkRequiredComponents()
            self.onMain {
                self.componentChecks = checks
            }
            self.log("component checks refreshed: cag=\(checks[0].installed) codex-acp=\(checks[1].installed)")
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

    func refreshSelectedSessionChatAsync() {
        if !beginRefresh(kind: "chat") {
            log("refresh skip kind=chat reason=inflight")
            return
        }
        log("refresh start kind=chat")
        runInBackground { [weak self] in
            defer {
                self?.endRefresh(kind: "chat")
                self?.log("refresh end kind=chat")
            }
            self?.refreshSelectedSessionChat()
        }
    }

    func refreshAccessUsersAsync() {
        if !beginRefresh(kind: "users") {
            log("refresh skip kind=users reason=inflight")
            return
        }
        log("refresh start kind=users")
        runInBackground { [weak self] in
            defer {
                self?.endRefresh(kind: "users")
                self?.log("refresh end kind=users")
            }
            self?.refreshAccessUsers()
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

    func refreshAccessUsers() {
        let t0 = Date()
        let res = cagJSON("users", timeoutSec: 8)
        guard let node = res.json, ((node["ok"] as? Bool) ?? false) else {
            onMain { [weak self] in
                self?.accessUsers = []
            }
            let ms = Int(Date().timeIntervalSince(t0) * 1000)
            log("refresh result kind=users status=empty elapsed_ms=\(ms)")
            return
        }
        let items = (node["items"] as? [[String: Any]] ?? []).compactMap { item -> AccessUserEntry? in
            let key = ((item["key"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            let channel = ((item["channel"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            let userID = ((item["user_id"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !key.isEmpty, !channel.isEmpty, !userID.isEmpty else { return nil }
            return AccessUserEntry(
                key: key,
                channel: channel,
                userID: userID,
                senderName: ((item["sender_name"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines),
                status: ((item["status"] as? String) ?? "pending").trimmingCharacters(in: .whitespacesAndNewlines),
                firstSeenAt: ((item["first_seen_at"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines),
                lastSeenAt: ((item["last_seen_at"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines),
                lastMessageID: ((item["last_message_id"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines),
                lastText: ((item["last_text"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines),
                threadID: ((item["thread_id"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines),
                conversationTitle: ((item["conversation_title"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            )
        }
        onMain { [weak self] in
            self?.accessUsers = items
        }
        let ms = Int(Date().timeIntervalSince(t0) * 1000)
        log("refresh result kind=users status=ok count=\(items.count) elapsed_ms=\(ms)")
    }

    func updateUserAccessStatus(entry: AccessUserEntry, status: String, completion: @escaping (Bool, String) -> Void) {
        let allowing = status.caseInsensitiveCompare("allowed") == .orderedSame
        let action = allowing ? "user-allow" : "user-block"
        let successMessage = allowing
            ? "Allowed \(entry.displayName). Future messages will be processed."
            : "Blocked \(entry.displayName). Future messages will be rejected."
        cagJSONAsync(
            action,
            args: ["--channel", entry.channel, "--user-id", entry.userID],
            timeoutSec: 8
        ) { [weak self] result in
            guard let self else { return }
            let ok = (result.json?["ok"] as? Bool) ?? false
            if ok {
                self.refreshAccessUsersAsync()
                self.refreshHealthChecksAsync()
                completion(true, successMessage)
                return
            }
            let fallback = allowing ? "Allow user failed." : "Block user failed."
            completion(false, self.cliErrorMessage(from: result.raw, fallback: fallback))
        }
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

        case .installCAG:
            installCAG()

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
                            deliveryStatus: self.messageDeliveryStatus(
                                role: (item["role"] as? String) ?? "assistant",
                                rawStatus: (item["status"] as? String) ?? ""
                            ),
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

    func applyChannelConfig(channel: ChannelType, values: [(String, String)]) {
        writeEnvValues(values)
        setChannel(channel)
        detailText = "Config updated for \(channel.title)."
    }

    func applyGlobalChannelConfig(channel: ChannelType, values: [(String, String)]) {
        writeUserEnvValues(values)
        setChannel(channel)
        detailText = "Config updated for \(channel.title) in ~/.cag/.env."
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
        let gatewayAddr = ((node["gateway_addr"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        onMain { [weak self] in
            guard let self else { return }
            if !nodeLog.isEmpty {
                self.currentLogFile = nodeLog
            } else if self.currentLogFile.isEmpty {
                self.currentLogFile = self.cfg.logFile
            }
            if !gatewayAddr.isEmpty {
                self.gatewayAddressText = gatewayAddr
            }
            self.activeChannelText = channel.title
            if status == "running" {
                self.statusText = "Running"
                let pidPart = (node["pid"] as? Int).map { "PID \($0)\n" } ?? ""
                let gatewayPart = self.gatewayAddressText.isEmpty ? "" : "Gatewayd: \(self.gatewayAddressText)\n"
                let lockPart = (node["lock_file"] as? String).map { "Lock: \($0)\n" } ?? ""
                let logPart = self.currentLogFile
                self.detailText = "\(gatewayPart)\(pidPart)Channel: \(channel.title)\n\(lockPart)Log: \(logPart)"
            } else {
                self.statusText = "Stopped"
                let gatewayPart = self.gatewayAddressText.isEmpty ? "" : "Gatewayd: \(self.gatewayAddressText)\n"
                let lockPart = (node["lock_file"] as? String).map { "\nLock: \($0)" } ?? ""
                self.detailText = "\(gatewayPart)Channel: \(channel.title)\nLog: \(self.currentLogFile)\(lockPart)"
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
                        channel: (item["channel"] as? String) ?? "-",
                        senderId: senderID.isEmpty ? "-" : senderID,
                        sender: senderName.isEmpty ? (senderID.isEmpty ? "-" : senderID) : senderName,
                        threadId: (item["thread_id"] as? String) ?? "-",
                        lastText: (item["last_message"] as? String) ?? "",
                        lastTime: (item["last_time"] as? String) ?? "",
                        workdir: (item["workdir"] as? String) ?? "",
                        latest: (item["latest"] as? Bool) ?? false
                    )
                )
            }
            onMain { [weak self] in
                guard let self else { return }
                self.sessions = built
                if let selected = self.selectedSessionKey, !self.sessions.contains(where: { $0.sessionKey == selected }) {
                    self.selectedSessionKey = nil
                }
                if self.selectedSessionKey == nil {
                    self.selectedSessionKey = self.sessions.first(where: { $0.latest })?.sessionKey ?? self.sessions.first?.sessionKey
                }
                self.refreshSelectedSessionWorkdir()
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
            self?.selectedSessionWorkdir = ""
            self?.chatMessages = []
            self?.timelineByMsgId = [:]
        }
        let ms = Int(Date().timeIntervalSince(t0) * 1000)
        log("refresh result kind=sessions status=empty elapsed_ms=\(ms)")
        return
    }

    func selectSession(_ key: String?) {
        selectedSessionKey = key
        refreshSelectedSessionWorkdir()
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
                if !msg.sourceMsgId.isEmpty &&
                    merged.contains(where: { $0.sourceMsgId == msg.sourceMsgId && $0.role == msg.role }) {
                    continue
                }
                merged.append(msg)
            }
        }
        return renderMessages(merged)
    }

    private func renderMessages(_ messages: [ChatMessage]) -> [ChatMessage] {
        let assistantFinalSourceIDs = Set(
            messages.compactMap { msg -> String? in
                guard msg.role == "assistant",
                      msg.deliveryStatus != .processing,
                      !msg.sourceMsgId.isEmpty else {
                    return nil
                }
                return msg.sourceMsgId
            }
        )

        var rendered = messages
        for msg in messages {
            guard msg.role == "user",
                  msg.deliveryStatus == .processing,
                  !msg.sourceMsgId.isEmpty,
                  !assistantFinalSourceIDs.contains(msg.sourceMsgId),
                  !rendered.contains(where: { $0.sourceMsgId == msg.sourceMsgId && $0.role == "assistant" && $0.deliveryStatus == .processing }) else {
                continue
            }

            rendered.append(
                ChatMessage(
                    id: "processing-\(msg.sourceMsgId)",
                    sourceMsgId: msg.sourceMsgId,
                    role: "assistant",
                    text: "",
                    time: msg.time,
                    deliveryStatus: .processing,
                    statusDetail: msg.statusDetail
                )
            )
        }

        return rendered.sorted { lhs, rhs in
            if lhs.time != rhs.time {
                return lhs.time < rhs.time
            }
            if lhs.role != rhs.role {
                if lhs.role == "user" {
                    return true
                }
                if rhs.role == "user" {
                    return false
                }
            }
            return lhs.id < rhs.id
        }
    }

    private func messageDeliveryStatus(role: String, rawStatus: String) -> MessageDeliveryStatus? {
        if role != "user" {
            return nil
        }
        switch rawStatus.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "sending":
            return .sending
        case "sent", "done", "ok", "success":
            return .sent
        case "processing", "running":
            return .processing
        case "failed", "error", "timeout":
            return .failed
        default:
            return nil
        }
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

    private func sendToSessionAsync(
        selectedSessionKey: String,
        baseSessionKey: String,
        text: String,
        sessionWorkdir: String,
        userMsgId: String,
        timeout: TimeInterval
    ) {
        var sendArgs = ["--session-key", baseSessionKey, "--message-id", userMsgId, "--text", text]
        if !sessionWorkdir.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            sendArgs.append(contentsOf: ["--workdir", sessionWorkdir])
        }
        cagJSONAsync("send", args: sendArgs, timeoutSec: timeout) { [weak self] result in
            guard let self else { return }
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
                let wdSuffix = sessionWorkdir.isEmpty ? "" : " [workdir]"
                self.detailText = elapsed > 0 ? "Session processed (\(elapsed)s).\(wdSuffix)" : "Session processed.\(wdSuffix)"
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

    private func clearSessionMappingAsync(baseSessionKey: String, completion: @escaping (Bool) -> Void) {
        cagJSONAsync("session-clear", args: ["--session-key", baseSessionKey]) { result in
            let ok = (result.json?["ok"] as? Bool) ?? false
            completion(ok)
        }
    }

    func sendLocalChat() {
        var text = localDraftText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        guard !localSending else { return }
        guard let session = selectedSessionEntry() else {
            detailText = "Select a gateway session first."
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
        let command = parseLocalCommand(text)

        if let cmd = command {
            if cmd.cmd == "/clear" {
                localSending = true
                localDraftText = ""
                clearSessionMappingAsync(baseSessionKey: baseSessionKey) { [weak self] cleared in
                    guard let self else { return }
                    self.localSending = false
                    self.appendLocalActionMessage(
                        cleared ? "Action /clear: session mapping reset." : "Action /clear failed: cannot update state file.",
                        sessionKey: selectedSessionKey
                    )
                    self.detailText = cleared ? "Gateway session mapping cleared." : "Failed to clear gateway session mapping."
                    self.refreshSessionsAsync()
                }
                return
            }
            if cmd.payload.isEmpty {
                localSending = true
                localDraftText = ""
                clearSessionMappingAsync(baseSessionKey: baseSessionKey) { [weak self] cleared in
                    guard let self else { return }
                    self.localSending = false
                    self.appendLocalActionMessage(
                        cleared ? "Action /new: session reset." : "Action /new warning: reset failed.",
                        sessionKey: selectedSessionKey
                    )
                    self.detailText = cleared ? "New gateway session started." : "Could not reset old gateway session."
                    self.refreshSessionsAsync()
                }
                return
            }
            text = cmd.payload
        }

        let sessionWorkdir = workdirForSessionKey(baseSessionKey)

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
        localSending = true
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
        if let cmd = command, cmd.cmd == "/new" {
            clearSessionMappingAsync(baseSessionKey: baseSessionKey) { [weak self] cleared in
                guard let self else { return }
                self.appendLocalActionMessage(
                    cleared ? "Action /new: session reset." : "Action /new warning: reset failed, sending anyway.",
                    sessionKey: selectedSessionKey
                )
                self.detailText = cleared ? "New session started." : "Could not reset old session; continuing send."
                self.sendToSessionAsync(
                    selectedSessionKey: selectedSessionKey,
                    baseSessionKey: baseSessionKey,
                    text: text,
                    sessionWorkdir: sessionWorkdir,
                    userMsgId: userMsgId,
                    timeout: timeout
                )
            }
            return
        }
        sendToSessionAsync(
            selectedSessionKey: selectedSessionKey,
            baseSessionKey: baseSessionKey,
            text: text,
            sessionWorkdir: sessionWorkdir,
            userMsgId: userMsgId,
            timeout: timeout
        )
    }

    func deleteAllSessions() {
        let res = cagJSON("sessions-delete-all")
        if ((res.json?["ok"] as? Bool) ?? false) {
            sessionWorkdirByKey.removeAll()
            saveSessionWorkdirByKey()
            selectedSessionKey = nil
            selectedSessionWorkdir = ""
            refreshSessionsAsync()
            detailText = "Deleted all gateway sessions."
        } else {
            detailText = "Delete failed: command failed."
        }
    }

    func deleteSession(key: String) {
        let targetKey = baseSessionKey(key)
        let res = cagJSON("session-delete", args: ["--session-key", targetKey])
        if ((res.json?["ok"] as? Bool) ?? false) {
            if selectedSessionKey == key {
                selectedSessionKey = nil
            }
            sessionWorkdirByKey.removeValue(forKey: targetKey)
            saveSessionWorkdirByKey()
            refreshSelectedSessionWorkdir()
            refreshSessionsAsync()
            detailText = "Deleted gateway session: \(targetKey)"
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

    func restartAsync(completion: @escaping (Bool, String) -> Void) {
        statusText = "Restarting"
        detailText = "Restarting gateway..."
        runInBackground { [weak self] in
            guard let self else { return }
            let shutdownRes = self.cagJSON("gatewayd-down", timeoutSec: 5)
            if shutdownRes.code != 0 {
                self.log("config restart gatewayd-down failed code=\(shutdownRes.code) raw=\(shutdownRes.raw)")
            }
            let bootRes = self.cagJSON("gatewayd-up", timeoutSec: 5)
            if bootRes.code != 0 || (bootRes.json?["ok"] as? Bool) == false {
                self.onMain {
                    self.statusText = "Restart failed"
                    let message = "Saved config, but gateway control plane restart failed.\n\(self.cliErrorMessage(from: bootRes.raw, fallback: "Gateway control plane restart failed."))"
                    self.detailText = message
                    completion(false, message)
                }
                return
            }
            let res = self.cagJSON("restart", timeoutSec: 15, direct: true)
            self.onMain {
                guard let node = res.json else {
                    self.statusText = "Restart failed"
                    let message = "Saved config, but restart returned invalid CLI output.\n\(res.raw.trimmingCharacters(in: .whitespacesAndNewlines))"
                    self.detailText = message
                    completion(false, message)
                    return
                }
                if ((node["ok"] as? Bool) ?? false) == false {
                    self.statusText = "Restart failed"
                    let errorText = ((node["error"] as? [String: Any])?["message"] as? String) ?? "Unknown error"
                    self.detailText = errorText
                    completion(false, "Saved config, but restart failed.\n\(errorText)")
                    return
                }
                self.refreshStatus()
                self.refreshHealthChecksAsync()
                self.refreshConfigPanel()
                let pidText = (node["pid"] as? Int).map { "PID: \($0)\n" } ?? ""
                let logPath = ((node["log_file"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                if !logPath.isEmpty {
                    self.currentLogFile = logPath
                }
                let shownLog = self.currentLogFile.isEmpty ? self.cfg.logFile : self.currentLogFile
                self.detailText = "\(pidText)Gateway restarted.\nLog: \(shownLog)"
                completion(true, "Saved and restarted \(self.selectedChannel.title).")
            }
        }
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

    private func shellEscape(_ raw: String) -> String {
        if raw.isEmpty {
            return "''"
        }
        return "'" + raw.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    private func cliErrorMessage(from raw: String, fallback: String) -> String {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let data = trimmed.data(using: .utf8) else {
            return fallback
        }
        if let node = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let error = node["error"] as? [String: Any],
           let message = (error["message"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines),
           !message.isEmpty {
            return message
        }
        return fallback
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

struct ActionIconBadge: View {
    let systemName: String
    let tint: Color

    var body: some View {
        Image(systemName: systemName)
            .font(.system(size: 13, weight: .semibold))
            .foregroundStyle(tint)
            .frame(width: 32, height: 32)
            .background(tint.opacity(0.12), in: RoundedRectangle(cornerRadius: 9))
            .overlay(
                RoundedRectangle(cornerRadius: 9)
                    .stroke(tint.opacity(0.24), lineWidth: 1)
            )
    }
}

struct IconActionButton: View {
    let systemName: String
    let helpText: String
    var tint: Color = .primary
    var disabled: Bool = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            ActionIconBadge(systemName: systemName, tint: disabled ? .secondary : tint)
        }
        .buttonStyle(.plain)
        .disabled(disabled)
        .help(helpText)
        .opacity(disabled ? 0.55 : 1)
    }
}

struct ChannelStatusPill: View {
    let channelText: String

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "slider.horizontal.3")
                .font(.system(size: 11, weight: .semibold))
            Text(channelText)
                .font(.system(size: 12, weight: .semibold))
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(Color.blue.opacity(0.14), in: Capsule())
        .overlay(Capsule().stroke(Color.blue.opacity(0.35), lineWidth: 1))
    }
}

struct AccessStatusPill: View {
    let status: String

    private var normalized: String {
        status.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    }

    private var tint: Color {
        switch normalized {
        case "allowed":
            return .green
        case "blocked":
            return .red
        default:
            return .orange
        }
    }

    private var title: String {
        switch normalized {
        case "allowed":
            return "Allowed"
        case "blocked":
            return "Blocked"
        default:
            return "Pending"
        }
    }

    var body: some View {
        Pill(text: title, color: tint)
    }
}

struct AccessUserRow: View {
    let entry: AccessUserEntry
    let busy: Bool
    let onAllow: () -> Void
    let onBlock: () -> Void

    private var channelTitle: String {
        ChannelType(rawValue: entry.channel)?.title ?? entry.channel.capitalized
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top, spacing: 10) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(entry.displayName)
                        .font(.system(size: 13, weight: .semibold))
                    HStack(spacing: 8) {
                        Text(channelTitle)
                            .font(.system(size: 11, weight: .medium))
                            .foregroundStyle(.secondary)
                        Text(entry.userID)
                            .font(.system(size: 11, design: .monospaced))
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                    }
                }
                Spacer()
                AccessStatusPill(status: entry.status)
            }
            if !entry.conversationTitle.isEmpty {
                Text(entry.conversationTitle)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }
            if !entry.lastText.isEmpty {
                Text(entry.lastText)
                    .font(.system(size: 12))
                    .foregroundStyle(.primary)
                    .lineLimit(3)
            }
            HStack(spacing: 12) {
                if !entry.lastSeenAt.isEmpty {
                    Text("Last seen \(LocalTimeDisplay.text(entry.lastSeenAt))")
                        .font(.system(size: 10))
                        .foregroundStyle(.tertiary)
                }
                if !entry.lastMessageID.isEmpty {
                    Text("msg \(entry.lastMessageID)")
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                }
            }
            HStack(spacing: 8) {
                ConfigActionButton(
                    systemName: busy ? "hourglass" : "checkmark.circle",
                    title: "Allow",
                    tint: .green,
                    disabled: busy || entry.status.lowercased() == "allowed",
                    action: onAllow
                )
                ConfigActionButton(
                    systemName: busy ? "hourglass" : "hand.raised.circle",
                    title: "Block",
                    tint: .red,
                    disabled: busy || entry.status.lowercased() == "blocked",
                    action: onBlock
                )
            }
        }
        .padding(14)
        .background(Color.gray.opacity(0.05), in: RoundedRectangle(cornerRadius: 12))
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

struct ComponentRow: View {
    let component: ComponentStatus
    let onInstall: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: component.installed ? "checkmark.circle.fill" : "xmark.octagon.fill")
                    .foregroundStyle(component.installed ? .green : .red)
                Text(component.name)
                    .font(.system(size: 12, weight: .semibold))
                Spacer()
                if !component.installed {
                    Button("Install") { onInstall() }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                }
            }
            Text(component.description)
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            if !component.installed {
                Text("Install: \(component.installCommand)")
                    .font(.system(size: 10))
                    .foregroundStyle(.tertiary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 4)
        .padding(.horizontal, 8)
        .background(component.installed ? Color.green.opacity(0.05) : Color.red.opacity(0.05), in: RoundedRectangle(cornerRadius: 8))
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
            Text("gateway key: \(session.sessionKey)")
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
        case .processing: return "Processing..."
        case .failed: return "Failed"
        case .action: return "Action"
        case .none: return ""
        }
    }

    private var deliveryColor: Color {
        switch message.deliveryStatus {
        case .sending: return .orange
        case .processing: return .blue
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
            if !message.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                Text(message.text)
                    .font(.system(size: 12))
                    .textSelection(.enabled)
            }
            if isUser, message.deliveryStatus != .processing, !deliveryText.isEmpty {
                Text(deliveryText)
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(deliveryColor)
            }
            if !isUser, message.deliveryStatus == .processing {
                Text(deliveryText)
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(deliveryColor)
            }
            if isUser, message.deliveryStatus == .failed, !message.statusDetail.isEmpty {
                Text(message.statusDetail)
                    .font(.system(size: 10))
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
            if !message.time.isEmpty {
                Text(LocalTimeDisplay.text(message.time))
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
                                        Text(LocalTimeDisplay.text(evt.time)).font(.system(size: 10)).foregroundStyle(.tertiary)
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

struct LogTailDrawerView: View {
    @ObservedObject var controller: LogTailController
    let onClose: () -> Void
    private let lineCountOptions = [120, 240, 500, 1000]

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .center, spacing: 10) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Live Logs")
                        .font(.system(size: 13, weight: .semibold))
                    Text(controller.logPath.isEmpty ? "(log path unavailable)" : controller.logPath)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .textSelection(.enabled)
                }
                Spacer()
                Toggle("Auto", isOn: $controller.autoRefresh)
                    .toggleStyle(.switch)
                    .frame(width: 90)
                Toggle("Follow", isOn: $controller.followTail)
                    .toggleStyle(.switch)
                    .frame(width: 100)
                Picker("Lines", selection: $controller.lineCount) {
                    ForEach(lineCountOptions, id: \.self) { count in
                        Text("\(count)").tag(count)
                    }
                }
                .labelsHidden()
                .pickerStyle(.menu)
                .frame(width: 90)
                IconActionButton(systemName: "arrow.clockwise", helpText: "Refresh log tail") { controller.refresh() }
                IconActionButton(systemName: "xmark", helpText: "Hide logs", tint: .secondary, action: onClose)
            }

            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        Text(controller.content.isEmpty ? "No log output yet." : controller.content)
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Color.clear
                            .frame(height: 1)
                            .id("log-bottom-anchor")
                    }
                    .padding(12)
                }
                .background(Color.black.opacity(0.035), in: RoundedRectangle(cornerRadius: 12))
                .onAppear {
                    if controller.followTail {
                        proxy.scrollTo("log-bottom-anchor", anchor: .bottom)
                    }
                }
                .onChange(of: controller.content) { _, _ in
                    guard controller.followTail else { return }
                    DispatchQueue.main.async {
                        proxy.scrollTo("log-bottom-anchor", anchor: .bottom)
                    }
                }
            }
        }
        .onChange(of: controller.lineCount) { _, _ in
            controller.refresh()
        }
        .padding(.horizontal, 14)
        .padding(.top, 12)
        .padding(.bottom, 14)
        .background(.thinMaterial)
    }
}

struct ChannelCard: View {
    let systemName: String
    let title: String
    let subtitle: String
    let selected: Bool
    let onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 8) {
                    Image(systemName: systemName)
                        .font(.system(size: 14, weight: .semibold))
                        .foregroundStyle(selected ? Color.accentColor : Color.secondary)
                    Text(title)
                        .font(.system(size: 13, weight: .semibold))
                }
                Text(subtitle)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(14)
            .background((selected ? Color.accentColor.opacity(0.16) : Color.gray.opacity(0.08)), in: RoundedRectangle(cornerRadius: 12))
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(selected ? Color.accentColor.opacity(0.5) : Color.clear, lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
    }
}

struct ConfigActionButton: View {
    let systemName: String
    let title: String
    var tint: Color = .accentColor
    var disabled: Bool = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: systemName)
                Text(title)
                    .font(.system(size: 12, weight: .semibold))
            }
            .foregroundStyle(disabled ? Color.secondary : tint)
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background((disabled ? Color.gray.opacity(0.08) : tint.opacity(0.12)), in: RoundedRectangle(cornerRadius: 10))
            .overlay(
                RoundedRectangle(cornerRadius: 10)
                    .stroke((disabled ? Color.gray.opacity(0.15) : tint.opacity(0.24)), lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
        .disabled(disabled)
        .opacity(disabled ? 0.65 : 1)
    }
}

struct ConfigField<Content: View>: View {
    let title: String
    let hint: String?
    let content: Content

    init(title: String, hint: String? = nil, @ViewBuilder content: () -> Content) {
        self.title = title
        self.hint = hint
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(.secondary)
            content
            if let hint, !hint.isEmpty {
                Text(hint)
                    .font(.system(size: 10))
                    .foregroundStyle(.tertiary)
            }
        }
    }
}

struct ConfigStatusBanner: View {
    let message: String
    let ok: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: ok ? "checkmark.circle.fill" : "info.circle.fill")
                .foregroundStyle(ok ? Color.green : Color.secondary)
            Text(message)
                .font(.system(size: 11))
                .foregroundStyle(ok ? Color.green : Color.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(10)
        .background((ok ? Color.green.opacity(0.08) : Color.gray.opacity(0.08)), in: RoundedRectangle(cornerRadius: 10))
    }
}

struct ConfigView: View {
    @ObservedObject var controller: GatewayController
    @State private var editingChannel: ChannelType
    @State private var guiACPAgentCmd: String
    @State private var imessageFetchCmd: String
    @State private var imessageSendCmd: String
    @State private var dingtalkSendMode: String
    @State private var dingtalkAppKey: String
    @State private var dingtalkAppSecret: String
    @State private var dingtalkAgentID: String
    @State private var dingtalkBotWebhook: String
    @State private var dingtalkDefaultTo: String
    @State private var configStatusMessage: String = ""
    @State private var configStatusOK: Bool = false
    @State private var configTestBusy: Bool = false
    @State private var configSaveBusy: Bool = false
    @State private var accessActionKey: String = ""

    init(controller: GatewayController, initialChannel: ChannelType? = nil) {
        self.controller = controller
        let channel = initialChannel ?? controller.selectedChannel
        _editingChannel = State(initialValue: channel)
        _guiACPAgentCmd = State(initialValue: controller.envValueOrDefault("ACP_AGENT_CMD", fallback: "codex-acp"))
        _imessageFetchCmd = State(initialValue: controller.envValueOrDefault("IMESSAGE_FETCH_CMD", fallback: "imsg fetch --json"))
        _imessageSendCmd = State(initialValue: controller.envValueOrDefault("IMESSAGE_SEND_CMD", fallback: "imsg send"))
        _dingtalkSendMode = State(initialValue: controller.envValueOrDefault("DINGTALK_SEND_MODE", fallback: "api"))
        _dingtalkAppKey = State(initialValue: controller.envValueOrDefault("DINGTALK_APP_KEY", fallback: ""))
        _dingtalkAppSecret = State(initialValue: controller.envValueOrDefault("DINGTALK_APP_SECRET", fallback: ""))
        _dingtalkAgentID = State(initialValue: controller.envValueOrDefault("DINGTALK_AGENT_ID", fallback: ""))
        _dingtalkBotWebhook = State(initialValue: controller.envValueOrDefault("DINGTALK_BOT_WEBHOOK", fallback: ""))
        _dingtalkDefaultTo = State(initialValue: controller.envValueOrDefault("DINGTALK_DEFAULT_TO_USER", fallback: ""))
    }

    private func reloadFromEnv() {
        editingChannel = controller.selectedChannel
        guiACPAgentCmd = controller.envValueOrDefault("ACP_AGENT_CMD", fallback: "codex-acp")
        imessageFetchCmd = controller.envValueOrDefault("IMESSAGE_FETCH_CMD", fallback: "imsg fetch --json")
        imessageSendCmd = controller.envValueOrDefault("IMESSAGE_SEND_CMD", fallback: "imsg send")
        dingtalkSendMode = controller.envValueOrDefault("DINGTALK_SEND_MODE", fallback: "api")
        dingtalkAppKey = controller.envValueOrDefault("DINGTALK_APP_KEY", fallback: "")
        dingtalkAppSecret = controller.envValueOrDefault("DINGTALK_APP_SECRET", fallback: "")
        dingtalkAgentID = controller.envValueOrDefault("DINGTALK_AGENT_ID", fallback: "")
        dingtalkBotWebhook = controller.envValueOrDefault("DINGTALK_BOT_WEBHOOK", fallback: "")
        dingtalkDefaultTo = controller.envValueOrDefault("DINGTALK_DEFAULT_TO_USER", fallback: "")
    }

    private func saveCurrentChannelConfig() {
        configSaveBusy = true
        configStatusOK = false
        configStatusMessage = "Saving config and restarting gateway..."
        switch editingChannel {
        case .command:
            controller.applyChannelConfig(
                channel: .command,
                values: [
                    ("ACP_AGENT_CMD", guiACPAgentCmd.trimmingCharacters(in: .whitespacesAndNewlines)),
                ]
            )
        case .imessage:
            controller.applyChannelConfig(
                channel: .imessage,
                values: [
                    ("IMESSAGE_FETCH_CMD", imessageFetchCmd.trimmingCharacters(in: .whitespacesAndNewlines)),
                    ("IMESSAGE_SEND_CMD", imessageSendCmd.trimmingCharacters(in: .whitespacesAndNewlines)),
                ]
            )
        case .dingtalk:
            controller.applyGlobalChannelConfig(
                channel: .dingtalk,
                values: [
                    ("DINGTALK_SEND_MODE", dingtalkSendMode.trimmingCharacters(in: .whitespacesAndNewlines)),
                    ("DINGTALK_APP_KEY", dingtalkAppKey.trimmingCharacters(in: .whitespacesAndNewlines)),
                    ("DINGTALK_APP_SECRET", dingtalkAppSecret.trimmingCharacters(in: .whitespacesAndNewlines)),
                    ("DINGTALK_AGENT_ID", dingtalkAgentID.trimmingCharacters(in: .whitespacesAndNewlines)),
                    ("DINGTALK_BOT_WEBHOOK", dingtalkBotWebhook.trimmingCharacters(in: .whitespacesAndNewlines)),
                    ("DINGTALK_DEFAULT_TO_USER", dingtalkDefaultTo.trimmingCharacters(in: .whitespacesAndNewlines)),
                ]
            )
        }
        controller.restartAsync { ok, message in
            configSaveBusy = false
            configStatusOK = ok
            configStatusMessage = message
        }
    }

    private func testActiveChannel() {
        configTestBusy = true
        configStatusMessage = "Testing active channel..."
        configStatusOK = false
        controller.testActiveChannelAsync { ok, message in
            configTestBusy = false
            configStatusOK = ok
            configStatusMessage = message
        }
    }

    private func updateAccess(_ entry: AccessUserEntry, status: String) {
        accessActionKey = entry.key
        configStatusOK = false
        configStatusMessage = status == "allowed" ? "Allowing \(entry.displayName)..." : "Blocking \(entry.displayName)..."
        controller.updateUserAccessStatus(entry: entry, status: status) { ok, message in
            accessActionKey = ""
            configStatusOK = ok
            configStatusMessage = message
        }
    }

    @ViewBuilder
    private var channelEditor: some View {
        switch editingChannel {
        case .command:
            VStack(alignment: .leading, spacing: 10) {
                Text("GUI Chat runs gateway-managed workspace sessions. Long-lived agent sessions stay in the agent CLI.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                Text("Gateway session workdir is no longer a global config item. Set it per workspace session from the chat panel.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                ConfigField(title: "ACP Command", hint: "Default is `codex-acp`. This controls the local ACP agent executable.") {
                    TextField("codex-acp", text: $guiACPAgentCmd)
                        .textFieldStyle(.roundedBorder)
                }
            }
        case .imessage:
            VStack(alignment: .leading, spacing: 10) {
                Text("Configure the `imsg` commands used for fetch/send.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                ConfigField(title: "Fetch Command", hint: "Command used to pull iMessage updates.") {
                    TextField("imsg fetch --json", text: $imessageFetchCmd)
                        .textFieldStyle(.roundedBorder)
                }
                ConfigField(title: "Send Command", hint: "Command used to send iMessage replies.") {
                    TextField("imsg send", text: $imessageSendCmd)
                        .textFieldStyle(.roundedBorder)
                }
            }
        case .dingtalk:
            VStack(alignment: .leading, spacing: 10) {
                Text("Configure DingTalk stream/app credentials and send routing.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                ConfigField(title: "Send Mode", hint: "Use `api` for enterprise app send, or `webhook` for bot webhook send.") {
                    Picker("Send Mode", selection: $dingtalkSendMode) {
                        Text("API").tag("api")
                        Text("Webhook").tag("webhook")
                    }
                    .pickerStyle(.segmented)
                }
                ConfigField(title: "App Key", hint: "Required for DingTalk stream ingress.") {
                    TextField("dingxxxxxxxx", text: $dingtalkAppKey)
                        .textFieldStyle(.roundedBorder)
                }
                ConfigField(title: "App Secret", hint: "Required for DingTalk stream ingress.") {
                    SecureField("App secret", text: $dingtalkAppSecret)
                        .textFieldStyle(.roundedBorder)
                }
                if dingtalkSendMode == "webhook" {
                    ConfigField(title: "Bot Webhook", hint: "Required when send mode is webhook.") {
                        TextField("https://oapi.dingtalk.com/robot/send?access_token=...", text: $dingtalkBotWebhook)
                            .textFieldStyle(.roundedBorder)
                    }
                } else {
                    ConfigField(title: "Agent ID", hint: "Required when send mode is api.") {
                        TextField("2536...", text: $dingtalkAgentID)
                            .textFieldStyle(.roundedBorder)
                    }
                    ConfigField(title: "Default User ID", hint: "Optional fallback recipient for startup greeting and direct sends.") {
                        TextField("Optional", text: $dingtalkDefaultTo)
                            .textFieldStyle(.roundedBorder)
                    }
                }
            }
        }
    }

    private func pathRow(title: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(.secondary)
            Text(value)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
                .background(Color.gray.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
        }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
            HStack {
                Text("Config")
                    .font(.title3.weight(.semibold))
                Spacer()
                IconActionButton(systemName: "arrow.clockwise", helpText: "Refresh config state") {
                    controller.refreshConfigPanel()
                }
            }
            HStack(spacing: 10) {
                ChannelCard(
                    systemName: "macwindow",
                    title: ChannelType.command.title,
                    subtitle: "Gateway-managed workspace sessions",
                    selected: editingChannel == .command
                ) { editingChannel = .command }
                ChannelCard(
                    systemName: "message",
                    title: ChannelType.imessage.title,
                    subtitle: "External iMessage bridge",
                    selected: editingChannel == .imessage
                ) { editingChannel = .imessage }
                ChannelCard(
                    systemName: "bubble.left.and.bubble.right",
                    title: ChannelType.dingtalk.title,
                    subtitle: "Stream ingress and send routing",
                    selected: editingChannel == .dingtalk
                ) { editingChannel = .dingtalk }
            }
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Text("Edit \(editingChannel.title)")
                        .font(.headline)
                    Spacer()
                    if controller.selectedChannel == editingChannel {
                        Pill(text: "Active", color: .green)
                    }
                }
                Text("Save will persist the selected channel config and restart the gateway automatically.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                channelEditor
                HStack(spacing: 10) {
                    ConfigActionButton(systemName: configSaveBusy ? "hourglass" : "checkmark.circle", title: configSaveBusy ? "Saving..." : "Save & Restart", tint: .green, disabled: configSaveBusy) {
                        saveCurrentChannelConfig()
                    }
                    ConfigActionButton(systemName: configTestBusy ? "hourglass" : "bolt.badge.checkmark", title: "Test Channel", tint: .orange, disabled: configTestBusy || configSaveBusy || controller.selectedChannel != editingChannel) {
                        testActiveChannel()
                    }
                    ConfigActionButton(systemName: "arrow.uturn.backward.circle", title: "Reload", tint: .secondary, disabled: configSaveBusy) {
                        reloadFromEnv()
                    }
                }
                if !configStatusMessage.isEmpty {
                    ConfigStatusBanner(message: configStatusMessage, ok: configStatusOK)
                }
            }
            .padding(16)
            .background(Color.gray.opacity(0.06), in: RoundedRectangle(cornerRadius: 14))
            Divider()
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Access Requests")
                        .font(.headline)
                    Text("Unknown channel users are recorded here. Allow or block them without editing env allowlists.")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if !controller.accessUsers.isEmpty {
                    Pill(text: "\(controller.accessUsers.filter { $0.status.lowercased() == "pending" }.count) pending", color: .orange)
                }
            }
            if controller.accessUsers.isEmpty {
                Text("No external users recorded yet.")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(14)
                    .background(Color.gray.opacity(0.04), in: RoundedRectangle(cornerRadius: 12))
            } else {
                VStack(alignment: .leading, spacing: 10) {
                    ForEach(controller.accessUsers) { entry in
                        AccessUserRow(
                            entry: entry,
                            busy: accessActionKey == entry.key || configSaveBusy
                        ) {
                            updateAccess(entry, status: "allowed")
                        } onBlock: {
                            updateAccess(entry, status: "blocked")
                        }
                    }
                }
            }
            Divider()
            Text("Workspace")
                .font(.headline)
            VStack(alignment: .leading, spacing: 10) {
                pathRow(
                    title: "Repo Root",
                    value: controller.repoRootPath
                )
                pathRow(
                    title: "Gateway Workdir",
                    value: controller.gatewayWorkdirPath
                )
                pathRow(
                    title: "Repo .env",
                    value: controller.envFilePath
                )
                pathRow(
                    title: "~/.cag/.env",
                    value: controller.globalEnvFilePath
                )
                pathRow(
                    title: "Current Log",
                    value: controller.effectiveLogPath
                )
            }
            .padding(14)
            .background(Color.gray.opacity(0.04), in: RoundedRectangle(cornerRadius: 12))
            Text("Gateway Session Tips: /new starts a fresh gateway session state, /clear resets the current gateway session mapping.")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            Divider()
            
            // Required Components Section
            Text("Required Components").font(.headline)
            ForEach(controller.componentChecks) { component in
                ComponentRow(component: component) {
                    if component.id == "cag" {
                        controller.installCAG()
                    } else if component.id == "codex-acp" {
                        controller.installCodexACP()
                    }
                    // Refresh after action
                    DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                        controller.refreshComponentChecksAsync()
                    }
                }
            }
            
            if controller.componentChecks.contains(where: { !$0.installed }) {
                Button("Refresh Status") {
                    controller.refreshComponentChecksAsync()
                }
                .font(.system(size: 12))
            }
            
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
        }
        .frame(width: 680, height: 720)
        .onAppear {
            controller.refreshConfigPanel()
            reloadFromEnv()
            controller.refreshAccessUsersAsync()
        }
        .onChange(of: controller.configReloadVersion) { _, _ in
            reloadFromEnv()
        }
    }
}

struct SetupCard<Content: View>: View {
    let title: String
    let bodyText: String
    let content: Content

    init(title: String, bodyText: String, @ViewBuilder content: () -> Content) {
        self.title = title
        self.bodyText = bodyText
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(title)
                .font(.title3.weight(.semibold))
            Text(bodyText)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
            content
        }
        .padding(18)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.gray.opacity(0.08), in: RoundedRectangle(cornerRadius: 14))
    }
}

struct InitialSetupView: View {
    @ObservedObject var controller: GatewayController

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                Image(systemName: "sparkles.rectangle.stack")
                    .font(.system(size: 22, weight: .semibold))
                VStack(alignment: .leading, spacing: 4) {
                    Text("Welcome")
                        .font(.title2.weight(.semibold))
                    Text("First-time setup for gateway-managed GUI chat")
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }

            SetupCard(
                title: "Initialize GUI Chat",
                bodyText: "The app will initialize the minimal gateway config for gateway-managed workspace chat. External channels can be configured later from the Config panel."
            ) {
                HStack(spacing: 10) {
                    Pill(text: "Default Channel: GUI Chat", color: .blue)
                    Pill(text: "Mode: Gateway Session", color: .green)
                }
                Text("Gateway session workdir is managed per workspace session from the chat panel. Long-lived agent sessions are not managed here.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                Button(controller.initialSetupBusy ? "Initializing..." : "Initialize") {
                    controller.completeInitialSetup()
                }
                .disabled(controller.initialSetupBusy)
            }

            if !controller.initialSetupMessage.isEmpty {
                Text(controller.initialSetupMessage)
                    .font(.system(size: 12))
                    .foregroundStyle(controller.needsInitialSetup ? Color.secondary : Color.green)
            }

            Spacer()
        }
        .padding(24)
        .frame(width: 860, height: 620)
        .onAppear {
            GUILogger.shared.log("initial setup view shown")
        }
    }
}

struct ContentView: View {
    @StateObject private var controller: GatewayController
    @StateObject private var logTailController: LogTailController
    private let refreshTimer = Timer.publish(every: 2.0, on: .main, in: .common).autoconnect()
    @State private var showConfig = false
    @State private var configInitialChannel: ChannelType
    @State private var showLogDrawer = false
    @State private var timelineMessage: ChatMessage?
    @State private var refreshTick: Int = 0

    init(controller: GatewayController) {
        _controller = StateObject(wrappedValue: controller)
        _logTailController = StateObject(wrappedValue: LogTailController(pathsProvider: { controller.liveLogPaths }))
        _configInitialChannel = State(initialValue: controller.selectedChannel)
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
            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Image(systemName: "app.badge.checkmark")
                        .font(.system(size: 18, weight: .semibold))
                    Text("CLI Agent Gateway")
                        .font(.title3.weight(.semibold))
                    Spacer()
                    Pill(text: controller.statusText, color: statusColor)
                    ChannelStatusPill(channelText: controller.activeChannelText)
                    IconActionButton(
                        systemName: "play.fill",
                        helpText: "Start gateway",
                        tint: .green,
                        disabled: controller.statusText == "Running"
                    ) {
                        controller.start()
                    }
                    .keyboardShortcut(.return, modifiers: [])
                    IconActionButton(
                        systemName: "stop.fill",
                        helpText: "Stop gateway",
                        tint: .orange,
                        disabled: controller.statusText != "Running"
                    ) {
                        controller.stop()
                    }
                    IconActionButton(
                        systemName: "arrow.clockwise",
                        helpText: "Restart gateway",
                        tint: .blue,
                        disabled: controller.statusText == "Blocked"
                    ) {
                        controller.restart()
                    }
                }
                if !controller.gatewayAddressText.isEmpty {
                    Text("gatewayd \(controller.gatewayAddressText)")
                        .font(.system(size: 11, weight: .medium, design: .monospaced))
                        .foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 18)
            .padding(.vertical, 14)
            .background(.ultraThinMaterial)

            HStack(spacing: 0) {
                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Text("Gateway Sessions")
                            .font(.title3.weight(.semibold))
                        Spacer()
                        IconActionButton(systemName: "plus", helpText: "Create gateway session", tint: .blue) {
                            controller.addSessionByPickingWorkdir()
                        }
                        IconActionButton(systemName: "folder", helpText: "Set selected gateway session workdir", tint: .secondary, disabled: controller.selectedSessionKey == nil) {
                            controller.updateSelectedSessionWorkdir()
                        }
                        IconActionButton(systemName: "trash", helpText: "Delete all gateway sessions", tint: .red) {
                            controller.deleteAllSessions()
                        }
                    }
                    Text("Gateway Session Workdir: \(controller.selectedSessionWorkdir.isEmpty ? "(not set)" : controller.selectedSessionWorkdir)")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
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
                                        Button("Delete Gateway Session") {
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
                                    Text("Select a gateway session to view chat history.")
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
                        TextField("Type here to chat in this gateway session...", text: $controller.localDraftText)
                            .textFieldStyle(.roundedBorder)
                            .disabled(controller.selectedSessionKey == nil)
                            .onSubmit {
                                controller.sendLocalChat()
                            }
                        IconActionButton(
                            systemName: controller.localSending ? "hourglass" : "paperplane.fill",
                            helpText: controller.localSending ? "Sending message" : "Send message",
                            tint: .blue,
                            disabled: controller.selectedSessionKey == nil
                                || controller.localSending
                                || controller.localDraftText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        ) {
                            controller.sendLocalChat()
                        }
                    }
                }
                .padding(14)
                .frame(minWidth: 620, idealWidth: 720)
            }

            if showLogDrawer {
                Divider()
                LogTailDrawerView(controller: logTailController) {
                    withAnimation(.easeOut(duration: 0.16)) {
                        showLogDrawer = false
                    }
                }
                .frame(height: 210)
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .frame(width: 1140, height: 700)
        .onAppear {
            GUILogger.shared.log("view onAppear bootstrap refresh")
            controller.refreshComponentChecksAsync()
            controller.ensureGatewaydForGUI()
            controller.refreshConfigPanel()
            controller.refreshSessionsAsync()
            controller.refreshAccessUsersAsync()
        }
        .onReceive(refreshTimer) { _ in
            refreshTick += 1
            controller.refreshStatusAsync()
            if controller.localSending {
                controller.refreshSelectedSessionChatAsync()
            }
            if refreshTick % 3 == 0 {
                controller.refreshSessionsAsync()
            }
            if refreshTick % 5 == 0 {
                controller.refreshAccessUsersAsync()
            }
        }
        .sheet(isPresented: $showConfig) {
            ConfigView(controller: controller, initialChannel: configInitialChannel)
        }
        .sheet(item: $timelineMessage) { msg in
            ProcessTimelineView(message: msg, events: controller.timeline(for: msg))
        }
        .onReceive(NotificationCenter.default.publisher(for: .guiOpenSettings)) { _ in
            configInitialChannel = controller.selectedChannel
            showConfig = true
        }
        .onReceive(NotificationCenter.default.publisher(for: .guiToggleLogDrawer)) { _ in
            withAnimation(.easeOut(duration: 0.16)) {
                showLogDrawer.toggle()
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: .guiOpenCurrentLog)) { _ in
            controller.openCurrentLog()
        }
        .onChange(of: showLogDrawer) { _, visible in
            if visible {
                logTailController.start()
            } else {
                logTailController.stop()
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.willTerminateNotification)) { _ in
            logTailController.stop()
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
        .commands {
            CommandMenu("Gateway") {
                Button("Open Settings") {
                    NotificationCenter.default.post(name: .guiOpenSettings, object: nil)
                }
                .keyboardShortcut(",", modifiers: .command)

                Button("Open Current Log") {
                    NotificationCenter.default.post(name: .guiOpenCurrentLog, object: nil)
                }
                .keyboardShortcut("j", modifiers: .command)

                Button("Toggle Live Logs") {
                    NotificationCenter.default.post(name: .guiToggleLogDrawer, object: nil)
                }
                .keyboardShortcut("j", modifiers: [.command, .shift])
            }
        }
    }
}

struct SetupRequiredView: View {
    @ObservedObject var controller: GatewayController
    let missingComponents: [ComponentStatus]
    @State private var isInstalling = false
    @State private var installProgress = ""

    var body: some View {
        HStack(spacing: 0) {
            // Left Panel - Global Status (Sidebar)
            VStack(alignment: .leading, spacing: 20) {
                VStack(alignment: .leading, spacing: 12) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .font(.system(size: 48))
                        .foregroundStyle(.orange)
                    
                    Text("Setup Required")
                        .font(.title2.weight(.bold))
                    
                    Text("Please install the following components to use the CLI Agent Gateway:")
                        .font(.system(size: 13))
                        .foregroundStyle(.secondary)
                }
                
                Spacer()
                
                // Status Summary
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(missingComponents) { component in
                        HStack(spacing: 8) {
                            Image(systemName: component.installed ? "checkmark.circle.fill" : "xmark.circle.fill")
                                .foregroundStyle(component.installed ? .green : .red)
                                .font(.system(size: 12))
                            Text("\(component.name): \(component.installed ? "Installed" : "Not Installed")")
                                .font(.system(size: 12))
                                .foregroundStyle(.secondary)
                        }
                    }
                }
                
                Button("Refresh Status") {
                    controller.refreshComponentChecksAsync()
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
            .padding(24)
            .frame(minWidth: 280, idealWidth: 320, maxWidth: 360)
            .background(Color(NSColor.controlBackgroundColor).opacity(0.5))
            
            Divider()
            
            // Right Panel - Component Cards
            VStack(alignment: .leading, spacing: 20) {
                Text("Components")
                    .font(.title3.weight(.semibold))
                    .padding(.top, 8)
                
                ScrollView {
                    VStack(spacing: 16) {
                        ForEach(missingComponents) { component in
                            ComponentCard(component: component) {
                                if component.id == "cag" {
                                    controller.installCAG()
                                    DispatchQueue.main.asyncAfter(deadline: .now() + 5) {
                                        controller.refreshComponentChecksAsync()
                                    }
                                } else if component.id == "codex-acp" {
                                    controller.installCodexACP()
                                }
                            }
                        }
                    }
                }
                
                Spacer()
                
                Text("After installation, restart the app.")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity)
            }
            .padding(32)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .frame(minWidth: 700, minHeight: 500)
    }
}

struct ComponentCard: View {
    let component: ComponentStatus
    let onAction: () -> Void
    
    var body: some View {
        HStack(alignment: .top, spacing: 16) {
            // Icon
            Image(systemName: component.id == "cag" ? "terminal.fill" : "brain")
                .font(.system(size: 28))
                .foregroundStyle(Color.accentColor)
                .frame(width: 48, height: 48)
                .background(Color.accentColor.opacity(0.1), in: RoundedRectangle(cornerRadius: 10))
            
            // Content
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text(component.name)
                        .font(.system(size: 16, weight: .semibold))
                    
                    if component.installed {
                        Text("Installed")
                            .font(.system(size: 10, weight: .medium))
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(Color.green.opacity(0.15), in: Capsule())
                            .foregroundStyle(.green)
                    }
                }
                
                Text(component.description)
                    .font(.system(size: 13))
                    .foregroundStyle(.secondary)
                
                if !component.installed {
                    // Code block
                    HStack {
                        Text(component.installCommand)
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundStyle(.primary)
                            .textSelection(.enabled)
                        Spacer()
                        Button {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(component.installCommand, forType: .string)
                        } label: {
                            Image(systemName: "doc.on.doc")
                                .font(.system(size: 12))
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(.secondary)
                    }
                    .padding(12)
                    .background(Color(NSColor.textBackgroundColor), in: RoundedRectangle(cornerRadius: 8))
                    .overlay(
                        RoundedRectangle(cornerRadius: 8)
                            .stroke(Color.gray.opacity(0.2), lineWidth: 1)
                    )
                }
            }
            
            // Action Button
            if !component.installed {
                if component.id == "cag" {
                    Button("Install") {
                        onAction()
                    }
                    .buttonStyle(.borderedProminent)
                } else {
                    Button("View Docs") {
                        onAction()
                    }
                    .buttonStyle(.bordered)
                }
            }
        }
        .padding(16)
        .background(Color(NSColor.controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .shadow(color: Color.black.opacity(0.05), radius: 4, x: 0, y: 2)
    }
}
