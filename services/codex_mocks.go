package services

import (
	"context"

	"nairid/clients"
)

// MockCodexClient implements the CodexClient interface for testing
type MockCodexClient struct {
	StartNewSessionFunc func(prompt string, options *clients.CodexOptions) (string, error)
	ContinueSessionFunc func(threadID, prompt string, options *clients.CodexOptions) (string, error)
}

func (m *MockCodexClient) StartNewSession(_ context.Context, prompt string, options *clients.CodexOptions, _ clients.ProgressCallback) (string, error) {
	if m.StartNewSessionFunc != nil {
		return m.StartNewSessionFunc(prompt, options)
	}
	return "", nil
}

func (m *MockCodexClient) ContinueSession(_ context.Context, threadID, prompt string, options *clients.CodexOptions, _ clients.ProgressCallback) (string, error) {
	if m.ContinueSessionFunc != nil {
		return m.ContinueSessionFunc(threadID, prompt, options)
	}
	return "", nil
}
