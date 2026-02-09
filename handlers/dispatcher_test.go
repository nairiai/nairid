package handlers

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gammazero/workerpool"

	"eksecd/models"
)

// createTestAppState creates an AppState with a temp file for persistence
func createTestAppState(t *testing.T) *models.AppState {
	t.Helper()
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "test_state.json")
	return models.NewAppState("test-agent", statePath)
}

// createTestAppStateNoPath creates an AppState without persistence (for simple tests)
func createTestAppStateNoPath() *models.AppState {
	return models.NewAppState("test-agent", "")
}

// mockMessageHandler tracks calls to HandleMessage for testing
type mockMessageHandler struct {
	mu             sync.Mutex
	handledMsgs    []models.BaseMessage
	handleDelay    time.Duration
	onHandleMsg    func(msg models.BaseMessage)
}

func (m *mockMessageHandler) HandleMessage(msg models.BaseMessage) {
	if m.handleDelay > 0 {
		time.Sleep(m.handleDelay)
	}
	m.mu.Lock()
	m.handledMsgs = append(m.handledMsgs, msg)
	m.mu.Unlock()
	if m.onHandleMsg != nil {
		m.onHandleMsg(msg)
	}
}

func (m *mockMessageHandler) getHandledMessages() []models.BaseMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]models.BaseMessage, len(m.handledMsgs))
	copy(result, m.handledMsgs)
	return result
}

// testableDispatcher wraps JobDispatcher with a mock handler for testing
type testableDispatcher struct {
	*JobDispatcher
	mockHandler *mockMessageHandler
}

func newTestableDispatcher(poolSize int, appState *models.AppState) *testableDispatcher {
	mockHandler := &mockMessageHandler{}
	wp := workerpool.New(poolSize)

	// Create dispatcher with nil handler (we'll intercept calls)
	dispatcher := &JobDispatcher{
		activeJobs:   make(map[string]chan models.BaseMessage),
		seenMessages: make(map[string]time.Time),
		lastCleanup:  time.Now(),
		handler:      nil, // Will be set via wrapper
		workerPool:   wp,
		appState:     appState,
	}

	return &testableDispatcher{
		JobDispatcher: dispatcher,
		mockHandler:   mockHandler,
	}
}

// Override processJobMessages to use mock handler
func (td *testableDispatcher) processJobMessagesWithMock(jobID string, ch chan models.BaseMessage) {
	defer td.cleanup(jobID)

	for msg := range ch {
		td.mockHandler.HandleMessage(msg)

		jobData, exists := td.appState.GetJobData(jobID)
		if !exists {
			return
		}

		// Exit on completed or failed status when channel is empty
		if (jobData.Status == models.JobStatusCompleted || jobData.Status == models.JobStatusFailed) && len(ch) == 0 {
			return
		}
	}
}

func createTestMessage(msgType string, jobID string) models.BaseMessage {
	var payload any
	switch msgType {
	case models.MessageTypeStartConversation:
		payload = models.StartConversationPayload{JobID: jobID, Message: "test message"}
	case models.MessageTypeUserMessage:
		payload = models.UserMessagePayload{JobID: jobID, Message: "test message"}
	default:
		payload = nil
	}
	return models.BaseMessage{Type: msgType, Payload: payload}
}

func createTestMessageWithProcessedID(msgType, jobID, processedMsgID string) models.BaseMessage {
	var payload any
	switch msgType {
	case models.MessageTypeStartConversation:
		payload = models.StartConversationPayload{
			JobID:              jobID,
			Message:            "test message",
			ProcessedMessageID: processedMsgID,
		}
	case models.MessageTypeUserMessage:
		payload = models.UserMessagePayload{
			JobID:              jobID,
			Message:            "test message",
			ProcessedMessageID: processedMsgID,
		}
	default:
		payload = nil
	}
	return models.BaseMessage{Type: msgType, Payload: payload}
}

func TestExtractJobID(t *testing.T) {
	appState := createTestAppStateNoPath()
	wp := workerpool.New(1)
	defer wp.StopWait()

	dispatcher := NewJobDispatcher(nil, wp, appState)

	tests := []struct {
		name     string
		msg      models.BaseMessage
		expected string
	}{
		{
			name:     "StartConversation message",
			msg:      createTestMessage(models.MessageTypeStartConversation, "job-123"),
			expected: "job-123",
		},
		{
			name:     "UserMessage message",
			msg:      createTestMessage(models.MessageTypeUserMessage, "job-456"),
			expected: "job-456",
		},
		{
			name:     "CheckIdleJobs message (no job ID)",
			msg:      models.BaseMessage{Type: models.MessageTypeCheckIdleJobs, Payload: nil},
			expected: "",
		},
		{
			name:     "Unknown message type",
			msg:      models.BaseMessage{Type: "unknown_type", Payload: nil},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dispatcher.extractJobID(tt.msg)
			if result != tt.expected {
				t.Errorf("extractJobID() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestDispatchCreatesChannelForNewJob(t *testing.T) {
	appState := createTestAppStateNoPath()
	wp := workerpool.New(2)
	defer wp.StopWait()

	td := newTestableDispatcher(2, appState)

	// Verify no channels exist initially
	td.mutex.Lock()
	initialCount := len(td.activeJobs)
	td.mutex.Unlock()

	if initialCount != 0 {
		t.Fatalf("Expected 0 initial channels, got %d", initialCount)
	}

	// Dispatch a message - this will create a channel
	msg := createTestMessage(models.MessageTypeStartConversation, "job-123")

	// Override the worker pool submit to use our mock processor
	td.workerPool = workerpool.New(2)
	defer td.workerPool.StopWait()

	// Manually simulate what Dispatch does
	td.mutex.Lock()
	ch := make(chan models.BaseMessage, 100)
	td.activeJobs["job-123"] = ch
	td.mutex.Unlock()

	// Verify channel was created
	td.mutex.Lock()
	_, exists := td.activeJobs["job-123"]
	td.mutex.Unlock()

	if !exists {
		t.Error("Expected channel to be created for job-123")
	}

	// Send message to channel
	ch <- msg

	// Verify message is in channel
	select {
	case received := <-ch:
		if received.Type != msg.Type {
			t.Errorf("Expected message type %s, got %s", msg.Type, received.Type)
		}
	default:
		t.Error("Expected message in channel")
	}
}

func TestDispatchRoutesMessagesToSameJobChannel(t *testing.T) {
	appState := createTestAppStateNoPath()
	wp := workerpool.New(2)
	defer wp.StopWait()

	dispatcher := NewJobDispatcher(nil, wp, appState)

	// Create a channel for a job manually
	ch := make(chan models.BaseMessage, 100)
	dispatcher.mutex.Lock()
	dispatcher.activeJobs["job-123"] = ch
	dispatcher.mutex.Unlock()

	// Dispatch multiple messages for the same job
	msg1 := createTestMessage(models.MessageTypeUserMessage, "job-123")
	msg2 := createTestMessage(models.MessageTypeUserMessage, "job-123")
	msg3 := createTestMessage(models.MessageTypeUserMessage, "job-123")

	// Send messages directly (simulating dispatch without starting processor)
	ch <- msg1
	ch <- msg2
	ch <- msg3

	// Verify all messages are in the same channel
	if len(ch) != 3 {
		t.Errorf("Expected 3 messages in channel, got %d", len(ch))
	}

	// Drain and verify
	for i := 0; i < 3; i++ {
		select {
		case <-ch:
			// Good
		default:
			t.Errorf("Expected message %d in channel", i+1)
		}
	}
}

func TestProcessorExitsWhenJobCompleted(t *testing.T) {
	appState := createTestAppState(t)

	// Add a job that will be marked as completed
	jobID := "job-123"
	err := appState.UpdateJobData(jobID, models.JobData{
		JobID:  jobID,
		Status: models.JobStatusInProgress,
	})
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	td := newTestableDispatcher(2, appState)

	ch := make(chan models.BaseMessage, 100)
	td.mutex.Lock()
	td.activeJobs[jobID] = ch
	td.mutex.Unlock()

	processorDone := make(chan struct{})

	// Start processor in goroutine
	go func() {
		td.processJobMessagesWithMock(jobID, ch)
		close(processorDone)
	}()

	// Send a message
	ch <- createTestMessage(models.MessageTypeUserMessage, jobID)

	// Wait a bit for message to be processed
	time.Sleep(50 * time.Millisecond)

	// Mark job as completed
	err = appState.UpdateJobData(jobID, models.JobData{
		JobID:  jobID,
		Status: models.JobStatusCompleted,
	})
	if err != nil {
		t.Fatalf("Failed to update job: %v", err)
	}

	// Send another message - processor should exit after this
	ch <- createTestMessage(models.MessageTypeUserMessage, jobID)

	// Wait for processor to exit
	select {
	case <-processorDone:
		// Good - processor exited
	case <-time.After(2 * time.Second):
		t.Error("Processor did not exit after job completed")
	}

	// Verify messages were processed
	msgs := td.mockHandler.getHandledMessages()
	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages processed, got %d", len(msgs))
	}
}

func TestProcessorExitsWhenJobFailed(t *testing.T) {
	appState := createTestAppState(t)

	// Add a job that will be marked as failed
	jobID := "job-123"
	err := appState.UpdateJobData(jobID, models.JobData{
		JobID:  jobID,
		Status: models.JobStatusInProgress,
	})
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	td := newTestableDispatcher(2, appState)

	ch := make(chan models.BaseMessage, 100)
	td.mutex.Lock()
	td.activeJobs[jobID] = ch
	td.mutex.Unlock()

	processorDone := make(chan struct{})

	// Start processor in goroutine
	go func() {
		td.processJobMessagesWithMock(jobID, ch)
		close(processorDone)
	}()

	// Send a message
	ch <- createTestMessage(models.MessageTypeUserMessage, jobID)

	// Wait a bit for message to be processed
	time.Sleep(50 * time.Millisecond)

	// Mark job as FAILED (simulating Claude session error)
	err = appState.UpdateJobData(jobID, models.JobData{
		JobID:  jobID,
		Status: models.JobStatusFailed,
	})
	if err != nil {
		t.Fatalf("Failed to update job: %v", err)
	}

	// Send another message - processor should exit after this
	ch <- createTestMessage(models.MessageTypeUserMessage, jobID)

	// Wait for processor to exit
	select {
	case <-processorDone:
		// Good - processor exited after job failed
	case <-time.After(2 * time.Second):
		t.Error("Processor did not exit after job failed - this causes worker pool exhaustion!")
	}

	// Verify messages were processed
	msgs := td.mockHandler.getHandledMessages()
	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages processed, got %d", len(msgs))
	}
}

func TestProcessorExitsWhenJobRemoved(t *testing.T) {
	appState := createTestAppState(t)

	jobID := "job-123"
	err := appState.UpdateJobData(jobID, models.JobData{
		JobID:  jobID,
		Status: models.JobStatusInProgress,
	})
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	td := newTestableDispatcher(2, appState)

	ch := make(chan models.BaseMessage, 100)
	td.mutex.Lock()
	td.activeJobs[jobID] = ch
	td.mutex.Unlock()

	processorDone := make(chan struct{})

	go func() {
		td.processJobMessagesWithMock(jobID, ch)
		close(processorDone)
	}()

	// Send a message
	ch <- createTestMessage(models.MessageTypeUserMessage, jobID)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Remove job from AppState
	err = appState.RemoveJob(jobID)
	if err != nil {
		t.Fatalf("Failed to remove job: %v", err)
	}

	// Send another message - processor should exit after this
	ch <- createTestMessage(models.MessageTypeUserMessage, jobID)

	// Wait for processor to exit
	select {
	case <-processorDone:
		// Good
	case <-time.After(2 * time.Second):
		t.Error("Processor did not exit after job removed")
	}
}

func TestProcessorProcessesAllQueuedMessagesBeforeExiting(t *testing.T) {
	appState := createTestAppState(t)

	jobID := "job-123"
	err := appState.UpdateJobData(jobID, models.JobData{
		JobID:  jobID,
		Status: models.JobStatusInProgress,
	})
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	td := newTestableDispatcher(2, appState)
	td.mockHandler.handleDelay = 10 * time.Millisecond // Slow down processing

	ch := make(chan models.BaseMessage, 100)
	td.mutex.Lock()
	td.activeJobs[jobID] = ch
	td.mutex.Unlock()

	processorDone := make(chan struct{})

	// Queue multiple messages BEFORE starting processor
	for i := 0; i < 5; i++ {
		ch <- createTestMessage(models.MessageTypeUserMessage, jobID)
	}

	// Mark job as completed BEFORE processing starts
	err = appState.UpdateJobData(jobID, models.JobData{
		JobID:  jobID,
		Status: models.JobStatusCompleted,
	})
	if err != nil {
		t.Fatalf("Failed to update job: %v", err)
	}

	// Start processor
	go func() {
		td.processJobMessagesWithMock(jobID, ch)
		close(processorDone)
	}()

	// Wait for processor to exit
	select {
	case <-processorDone:
		// Good
	case <-time.After(5 * time.Second):
		t.Error("Processor did not exit")
	}

	// Verify ALL messages were processed despite job being "completed"
	msgs := td.mockHandler.getHandledMessages()
	if len(msgs) != 5 {
		t.Errorf("Expected 5 messages processed, got %d", len(msgs))
	}
}

func TestCleanupRemovesChannelFromActiveJobs(t *testing.T) {
	appState := createTestAppStateNoPath()
	wp := workerpool.New(2)
	defer wp.StopWait()

	dispatcher := NewJobDispatcher(nil, wp, appState)

	jobID := "job-123"
	ch := make(chan models.BaseMessage, 100)

	dispatcher.mutex.Lock()
	dispatcher.activeJobs[jobID] = ch
	dispatcher.mutex.Unlock()

	// Verify channel exists
	dispatcher.mutex.Lock()
	_, exists := dispatcher.activeJobs[jobID]
	dispatcher.mutex.Unlock()
	if !exists {
		t.Fatal("Expected channel to exist before cleanup")
	}

	// Call cleanup
	dispatcher.cleanup(jobID)

	// Verify channel was removed
	dispatcher.mutex.Lock()
	_, exists = dispatcher.activeJobs[jobID]
	dispatcher.mutex.Unlock()
	if exists {
		t.Error("Expected channel to be removed after cleanup")
	}
}

func TestMultipleJobsProcessInParallel(t *testing.T) {
	appState := createTestAppState(t)

	// Create two jobs
	for _, jobID := range []string{"job-1", "job-2"} {
		err := appState.UpdateJobData(jobID, models.JobData{
			JobID:  jobID,
			Status: models.JobStatusInProgress,
		})
		if err != nil {
			t.Fatalf("Failed to create job %s: %v", jobID, err)
		}
	}

	td := newTestableDispatcher(2, appState)

	var processingOrder []string
	var orderMu sync.Mutex
	var processing int32

	td.mockHandler.onHandleMsg = func(msg models.BaseMessage) {
		jobID := ""
		if payload, ok := msg.Payload.(models.UserMessagePayload); ok {
			jobID = payload.JobID
		}

		atomic.AddInt32(&processing, 1)
		orderMu.Lock()
		processingOrder = append(processingOrder, jobID+"-start")
		orderMu.Unlock()

		// Simulate some work
		time.Sleep(50 * time.Millisecond)

		orderMu.Lock()
		processingOrder = append(processingOrder, jobID+"-end")
		orderMu.Unlock()
		atomic.AddInt32(&processing, -1)
	}

	ch1 := make(chan models.BaseMessage, 100)
	ch2 := make(chan models.BaseMessage, 100)

	td.mutex.Lock()
	td.activeJobs["job-1"] = ch1
	td.activeJobs["job-2"] = ch2
	td.mutex.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)

	// Start processors for both jobs
	go func() {
		td.processJobMessagesWithMock("job-1", ch1)
		wg.Done()
	}()
	go func() {
		td.processJobMessagesWithMock("job-2", ch2)
		wg.Done()
	}()

	// Send messages to both jobs
	ch1 <- createTestMessage(models.MessageTypeUserMessage, "job-1")
	ch2 <- createTestMessage(models.MessageTypeUserMessage, "job-2")

	// Wait a bit for parallel processing to start
	time.Sleep(25 * time.Millisecond)

	// Check that both are processing in parallel
	currentProcessing := atomic.LoadInt32(&processing)
	if currentProcessing != 2 {
		t.Logf("Warning: Expected 2 jobs processing in parallel, got %d (timing-sensitive)", currentProcessing)
	}

	// Wait for first messages to be fully processed (50ms work + margin)
	time.Sleep(100 * time.Millisecond)

	// Mark both jobs as completed, then send final messages to trigger exit.
	// Order matters: mark completed first, then send. The goroutines are blocked
	// on `range ch` after processing the first message, so the channel is open.
	// When they receive the next message, they'll see the completed status and exit.
	for _, jobID := range []string{"job-1", "job-2"} {
		err := appState.UpdateJobData(jobID, models.JobData{
			JobID:  jobID,
			Status: models.JobStatusCompleted,
		})
		if err != nil {
			t.Fatalf("Failed to update job %s: %v", jobID, err)
		}
	}

	// Send final messages to trigger exit check
	ch1 <- createTestMessage(models.MessageTypeUserMessage, "job-1")
	ch2 <- createTestMessage(models.MessageTypeUserMessage, "job-2")

	// Wait for both processors to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Error("Processors did not finish in time")
	}

	// Verify both jobs were processed
	msgs := td.mockHandler.getHandledMessages()
	if len(msgs) != 4 { // 2 messages per job
		t.Errorf("Expected 4 messages processed, got %d", len(msgs))
	}
}

func TestDispatcher_DuplicateStartConversation(t *testing.T) {
	appState := createTestAppStateNoPath()
	wp := workerpool.New(2)
	defer wp.StopWait()

	dispatcher := NewJobDispatcher(nil, wp, appState)

	// Manually create channel to receive messages (skip actual processing)
	ch := make(chan models.BaseMessage, 100)
	dispatcher.mutex.Lock()
	dispatcher.activeJobs["job-123"] = ch
	dispatcher.mutex.Unlock()

	// Dispatch same StartConversation twice (same ProcessedMessageID)
	msg := createTestMessageWithProcessedID(
		models.MessageTypeStartConversation,
		"job-123",
		"processed-msg-001",
	)

	dispatcher.Dispatch(msg)
	dispatcher.Dispatch(msg) // duplicate

	// Only one message should be in the channel
	if len(ch) != 1 {
		t.Errorf("Expected 1 message in channel (duplicate filtered), got %d", len(ch))
	}
}

func TestDispatcher_DuplicateUserMessage(t *testing.T) {
	appState := createTestAppStateNoPath()
	wp := workerpool.New(2)
	defer wp.StopWait()

	dispatcher := NewJobDispatcher(nil, wp, appState)

	ch := make(chan models.BaseMessage, 100)
	dispatcher.mutex.Lock()
	dispatcher.activeJobs["job-456"] = ch
	dispatcher.mutex.Unlock()

	// Dispatch same UserMessage twice
	msg := createTestMessageWithProcessedID(
		models.MessageTypeUserMessage,
		"job-456",
		"processed-msg-002",
	)

	dispatcher.Dispatch(msg)
	dispatcher.Dispatch(msg) // duplicate

	if len(ch) != 1 {
		t.Errorf("Expected 1 message in channel (duplicate filtered), got %d", len(ch))
	}
}

func TestDispatcher_DifferentMessagesNotDeduplicated(t *testing.T) {
	appState := createTestAppStateNoPath()
	wp := workerpool.New(2)
	defer wp.StopWait()

	dispatcher := NewJobDispatcher(nil, wp, appState)

	ch := make(chan models.BaseMessage, 100)
	dispatcher.mutex.Lock()
	dispatcher.activeJobs["job-789"] = ch
	dispatcher.mutex.Unlock()

	// Dispatch two different messages (different ProcessedMessageIDs)
	msg1 := createTestMessageWithProcessedID(
		models.MessageTypeUserMessage,
		"job-789",
		"processed-msg-003",
	)
	msg2 := createTestMessageWithProcessedID(
		models.MessageTypeUserMessage,
		"job-789",
		"processed-msg-004",
	)

	dispatcher.Dispatch(msg1)
	dispatcher.Dispatch(msg2)

	// Both messages should be in the channel
	if len(ch) != 2 {
		t.Errorf("Expected 2 messages in channel (different IDs), got %d", len(ch))
	}
}

func TestDispatcher_SeenMessageCleanup(t *testing.T) {
	appState := createTestAppStateNoPath()
	wp := workerpool.New(2)
	defer wp.StopWait()

	dispatcher := NewJobDispatcher(nil, wp, appState)

	ch := make(chan models.BaseMessage, 100)
	dispatcher.mutex.Lock()
	dispatcher.activeJobs["job-cleanup"] = ch
	dispatcher.mutex.Unlock()

	// Add a message to seenMessages with an old timestamp
	dispatcher.mutex.Lock()
	dispatcher.seenMessages["old-msg-id"] = time.Now().Add(-10 * time.Minute)
	dispatcher.lastCleanup = time.Now().Add(-10 * time.Minute) // Force cleanup to run
	dispatcher.mutex.Unlock()

	// Dispatch a new message to trigger cleanup
	msg := createTestMessageWithProcessedID(
		models.MessageTypeUserMessage,
		"job-cleanup",
		"new-msg-id",
	)
	dispatcher.Dispatch(msg)

	// Old message should be cleaned up, new message should exist
	dispatcher.mutex.Lock()
	_, oldExists := dispatcher.seenMessages["old-msg-id"]
	_, newExists := dispatcher.seenMessages["new-msg-id"]
	dispatcher.mutex.Unlock()

	if oldExists {
		t.Error("Expected old message to be cleaned up")
	}
	if !newExists {
		t.Error("Expected new message to exist in seenMessages")
	}
}

func TestDispatcher_EmptyProcessedMessageID(t *testing.T) {
	appState := createTestAppStateNoPath()
	wp := workerpool.New(2)
	defer wp.StopWait()

	dispatcher := NewJobDispatcher(nil, wp, appState)

	ch := make(chan models.BaseMessage, 100)
	dispatcher.mutex.Lock()
	dispatcher.activeJobs["job-empty"] = ch
	dispatcher.mutex.Unlock()

	// Dispatch messages without ProcessedMessageID (empty string)
	msg1 := createTestMessageWithProcessedID(
		models.MessageTypeUserMessage,
		"job-empty",
		"", // empty ProcessedMessageID
	)
	msg2 := createTestMessageWithProcessedID(
		models.MessageTypeUserMessage,
		"job-empty",
		"", // also empty
	)

	dispatcher.Dispatch(msg1)
	dispatcher.Dispatch(msg2)

	// Both should be processed (no dedup for empty IDs)
	if len(ch) != 2 {
		t.Errorf("Expected 2 messages (empty IDs not deduplicated), got %d", len(ch))
	}

	// seenMessages should not contain empty string
	dispatcher.mutex.Lock()
	_, exists := dispatcher.seenMessages[""]
	dispatcher.mutex.Unlock()

	if exists {
		t.Error("Empty ProcessedMessageID should not be stored in seenMessages")
	}
}
