package main

import (
	"time"

	"github.com/sirupsen/logrus"
)

var (
	autoStopTimer   *time.Timer
	pteroController *PterodactylController
)

func setPterodactylController(p *PterodactylController) {
	pteroController = p
}

func startServerIfNotRunning() {
	if pteroController == nil {
		return
	}
	status, err := pteroController.GetStatus()
	if err != nil {
		logrus.Errorf("Could not get server status, attempting start anyway: %v", err)
		if err2 := pteroController.StartServer(); err2 != nil {
			logrus.Errorf("Error starting server: %v", err2)
		}
		return
	}

	if status == "running" || status == "starting" {
		logrus.Infof("Server is already %s, no need to start.", status)
		return
	}

	logrus.Infof("Server is in state: %s, sending StartServer command...", status)
	if err2 := pteroController.StartServer(); err2 != nil {
		logrus.Errorf("Error starting server: %v", err2)
	}
}

func cancelAutoStopTimer() {
	if autoStopTimer != nil {
		if autoStopTimer.Stop() {
			logrus.Infof("Auto-stop timer cancelled (we have new session(s) again).")
		}
		autoStopTimer = nil
	}
}

func scheduleAutoStop() {
	cancelAutoStopTimer()
	autoStopTimer = time.AfterFunc(globalConfig.AutoStopDelay, func() {
		if sessionCount() == 0 && pteroController != nil {
			logrus.Infof("No sessions for %v. Stopping server via Pterodactyl.", globalConfig.AutoStopDelay)
			if err := pteroController.StopServer(); err != nil {
				logrus.Errorf("Error stopping server: %v", err)
			}
		} else {
			logrus.Infof("Auto-stop timer fired, but session count is now %d. Not stopping.", sessionCount())
		}
	})
	logrus.Infof("All sessions closed. Scheduled auto-stop in %v...", globalConfig.AutoStopDelay)
}
