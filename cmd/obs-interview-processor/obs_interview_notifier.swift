import AppKit
import Foundation
import UserNotifications

let openDirectoryKey = "open_directory"

struct Arguments {
    let title: String
    let message: String
    let directory: String

    static func parse(_ values: [String]) -> Arguments? {
        var parsed: [String: String] = [:]
        var index = 0
        while index < values.count {
            let key = values[index]
            guard ["--title", "--message", "--open-dir"].contains(key), index + 1 < values.count else {
                return nil
            }
            parsed[key] = values[index + 1]
            index += 2
        }
        guard
            let title = parsed["--title"], !title.isEmpty,
            let message = parsed["--message"], !message.isEmpty,
            let directory = parsed["--open-dir"], !directory.isEmpty
        else {
            return nil
        }
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: directory, isDirectory: &isDirectory), isDirectory.boolValue else {
            return nil
        }
        return Arguments(title: title, message: message, directory: directory)
    }
}

final class NotificationDelegate: NSObject, UNUserNotificationCenterDelegate {
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        defer {
            completionHandler()
            DispatchQueue.main.async { NSApp.terminate(nil) }
        }
        guard response.actionIdentifier == UNNotificationDefaultActionIdentifier,
              let directory = response.notification.request.content.userInfo[openDirectoryKey] as? String else {
            return
        }
        NSWorkspace.shared.open(URL(fileURLWithPath: directory, isDirectory: true))
    }
}

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(1)
}

guard let arguments = Arguments.parse(Array(CommandLine.arguments.dropFirst())) else {
    fail("usage: obs-interview-notifier --title TITLE --message MESSAGE --open-dir DIRECTORY")
}

let application = NSApplication.shared
application.setActivationPolicy(.accessory)
let center = UNUserNotificationCenter.current()
let delegate = NotificationDelegate()
center.delegate = delegate

center.requestAuthorization(options: [.alert, .sound]) { granted, error in
    if let error = error {
        fail("request notification authorization: \(error)")
    }
    guard granted else {
        fail("notification authorization was denied")
    }
    let content = UNMutableNotificationContent()
    content.title = arguments.title
    content.body = arguments.message
    content.sound = .default
    content.userInfo = [openDirectoryKey: arguments.directory]
    let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
    center.add(request) { addError in
        if let addError = addError {
            fail("deliver notification: \(addError)")
        }
    }
}

application.run()
