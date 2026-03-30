package application

import (
	"time"
)

// logChannelStats logs the current state of all channels for debugging memory issues
// This function reads values without lock - all accessed fields are either:
// 1. Thread-safe (channel len() operations)
// 2. Read-only after initialization (sizes)
// 3. Best-effort check (streamOK - may be slightly stale but acceptable for monitoring)
func (sm *stream) logChannelStats() {
	defer func() {
		if r := recover(); r != nil {
			// If logging panics, don't crash the monitor
			sm.log.Errorf("channel stats logging panic: %v, clientID: %d, dialogueID: %d", r, sm.cn.ClientID(), sm.dg.DialogueID())
		}
	}()

	// Read channel lengths (len() is thread-safe, no lock needed)
	writeInLen := len(sm.writeInCh)
	messageLen := len(sm.messageCh)
	streamLen := len(sm.streamCh)
	failedLen := len(sm.failedCh)

	// Read sizes without lock - these are read-only after initialization
	// streamOK may be slightly stale but that's acceptable for monitoring purposes
	writeInSize := sm.writeInSize
	messageChSize := sm.messageChSize
	streamChSize := sm.streamChSize
	failedChSize := sm.failedChSize
	streamOK := sm.streamOK
	clientID := sm.cn.ClientID()
	dialogueID := sm.dg.DialogueID()

	if !streamOK {
		return
	}

	// Calculate usage percentages
	writeInUsage := float64(writeInLen) / float64(writeInSize) * 100
	messageUsage := float64(messageLen) / float64(messageChSize) * 100
	streamUsage := float64(streamLen) / float64(streamChSize) * 100
	failedUsage := float64(failedLen) / float64(failedChSize) * 100

	// Use WARN level if any channel is > 80% full, otherwise INFO
	if writeInUsage > 80 || messageUsage > 80 || streamUsage > 80 || failedUsage > 80 {
		sm.log.Warnf("stream channel stats (HIGH USAGE), clientID: %d, dialogueID: %d, writeInCh: %d/%d (%.1f%%), messageCh: %d/%d (%.1f%%), streamCh: %d/%d (%.1f%%), failedCh: %d/%d (%.1f%%)",
			clientID, dialogueID,
			writeInLen, writeInSize, writeInUsage,
			messageLen, messageChSize, messageUsage,
			streamLen, streamChSize, streamUsage,
			failedLen, failedChSize, failedUsage)
	} else {
		sm.log.Infof("stream channel stats, clientID: %d, dialogueID: %d, writeInCh: %d/%d (%.1f%%), messageCh: %d/%d (%.1f%%), streamCh: %d/%d (%.1f%%), failedCh: %d/%d (%.1f%%)",
			clientID, dialogueID,
			writeInLen, writeInSize, writeInUsage,
			messageLen, messageChSize, messageUsage,
			streamLen, streamChSize, streamUsage,
			failedLen, failedChSize, failedUsage)
	}
}

// startChannelMonitor starts a goroutine that periodically logs channel statistics
// interval: logging interval (default: 30 seconds if <= 0)
// The goroutine will exit when streamOK becomes false (checked in logChannelStats)
func (sm *stream) startChannelMonitor(interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-sm.monitorStop:
				return
			case <-ticker.C:
				if !sm.streamOK {
					return
				}
				sm.logChannelStats()
			}
		}
	}()
}
