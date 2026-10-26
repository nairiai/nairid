package services

import (
	"context"

	"nairid/clients"
)

// MockClaudeClient implements the ClaudeClient interface for testing
type MockClaudeClient struct {
	StartNewSessionFunc func(prompt string, options *clients.ClaudeOptions) (string, error)
	ContinueSessionFunc func(sessionID, prompt string, options *clients.ClaudeOptions) (string, error)
}

func (m *MockClaudeClient) StartNewSession(_ context.Context, prompt string, options *clients.ClaudeOptions, _ clients.ProgressCallback) (string, error) {
	if m.StartNewSessionFunc != nil {
		return m.StartNewSessionFunc(prompt, options)
	}
	return "", nil
}

func (m *MockClaudeClient) ContinueSession(_ context.Context, sessionID, prompt string, options *clients.ClaudeOptions, _ clients.ProgressCallback) (string, error) {
	if m.ContinueSessionFunc != nil {
		return m.ContinueSessionFunc(sessionID, prompt, options)
	}
	return "", nil
}
