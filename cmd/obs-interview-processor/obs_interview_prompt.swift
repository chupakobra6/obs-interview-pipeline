import AppKit
import Foundation

struct PromptArguments {
    let processor: String
    let config: String
    let recording: String
    let deleteSourceDefault: Bool

    static func parse(_ values: [String]) -> PromptArguments? {
        var parsed: [String: String] = [:]
        var index = 0
        while index < values.count {
            let key = values[index]
            guard ["--processor", "--config", "--recording", "--delete-source-default"].contains(key), index + 1 < values.count else {
                return nil
            }
            parsed[key] = values[index + 1]
            index += 2
        }
        guard
            let processor = parsed["--processor"], FileManager.default.isExecutableFile(atPath: processor),
            let config = parsed["--config"], FileManager.default.fileExists(atPath: config),
            let recording = parsed["--recording"], FileManager.default.fileExists(atPath: recording),
            let deleteValue = parsed["--delete-source-default"],
            let deleteSourceDefault = Bool(deleteValue)
        else {
            return nil
        }
        return PromptArguments(
            processor: processor,
            config: config,
            recording: recording,
            deleteSourceDefault: deleteSourceDefault
        )
    }
}

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(1)
}

func showError(_ message: String) {
    let alert = NSAlert()
    alert.alertStyle = .critical
    alert.messageText = "Не удалось поставить запись в обработку"
    alert.informativeText = message
    alert.addButton(withTitle: "Закрыть")
    alert.runModal()
}

guard let arguments = PromptArguments.parse(Array(CommandLine.arguments.dropFirst())) else {
    fail("usage: obs-interview-prompt --processor PATH --config PATH --recording PATH --delete-source-default true|false")
}

let application = NSApplication.shared
application.setActivationPolicy(.accessory)
application.finishLaunching()
application.activate(ignoringOtherApps: true)

let deleteSource = NSButton(checkboxWithTitle: "Удалить исходник после успешной проверки", target: nil, action: nil)
deleteSource.state = arguments.deleteSourceDefault ? .on : .off
let mergeAudio = NSButton(checkboxWithTitle: "Свести аудиодорожки в одну", target: nil, action: nil)
mergeAudio.state = .off

let choices = NSStackView(views: [deleteSource, mergeAudio])
choices.orientation = .vertical
choices.alignment = .leading
choices.spacing = 8
choices.frame = NSRect(x: 0, y: 0, width: 430, height: 48)

let alert = NSAlert()
alert.alertStyle = .informational
alert.messageText = "Обработать запись OBS?"
alert.informativeText = URL(fileURLWithPath: arguments.recording).lastPathComponent
alert.accessoryView = choices
alert.addButton(withTitle: "Сжать и расшифровать")
alert.addButton(withTitle: "Оставить без обработки")

guard alert.runModal() == .alertFirstButtonReturn else {
    exit(0)
}

let task = Process()
let errorPipe = Pipe()
task.executableURL = URL(fileURLWithPath: arguments.processor)
task.arguments = [
    "enqueue",
    "--config", arguments.config,
    "--delete-source=\(deleteSource.state == .on)",
    "--audio-mode=\(mergeAudio.state == .on ? "merge" : "preserve")",
    arguments.recording,
]
task.standardError = errorPipe

do {
    try task.run()
    task.waitUntilExit()
    guard task.terminationStatus == 0 else {
        let payload = errorPipe.fileHandleForReading.readDataToEndOfFile()
        let detail = String(data: payload, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
        showError(detail?.isEmpty == false ? detail! : "Обработчик завершился с кодом \(task.terminationStatus).")
        exit(1)
    }
} catch {
    showError(error.localizedDescription)
    exit(1)
}
