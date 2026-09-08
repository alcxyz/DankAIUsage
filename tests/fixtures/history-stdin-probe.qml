import QtQuick
import Quickshell
import Quickshell.Io

ShellRoot {
    property string captured: ""

    Process {
        id: probe
        command: ["head", "-n", "1"]
        stdinEnabled: true
        running: true
        onStarted: {
            write(JSON.stringify({
                groupId: "group-probe",
                reason: "unknown",
                note: "literal; $(not a shell)"
            }) + "\n")
            stdinEnabled = false
        }
        stdout: SplitParser { onRead: data => { captured += data } }
        onExited: (exitCode, exitStatus) => {
            console.info("HISTORY-STDIN:" + captured)
            Qt.quit()
        }
    }

    Timer {
        interval: 5000
        running: true
        onTriggered: Qt.quit()
    }
}
