package services

import "nairid/clients"

// MockOpenCodeClient implements the OpenCodeClient interface for testing
type MockOpenCodeClient struct {
	StartNewSessionFunc func(prompt string, options *clients.OpenCodeOptions) (string, error)
	ContinueSessionFunc func(sessionID, prompt string, options *clients.OpenCodeOptions) (string, error)
}

func (m *MockOpenCodeClient) StartNewSession(prompt string, options *clients.OpenCodeOptions) (string, error) {
	if m.StartNewSessionFunc != nil {
		return m.StartNewSessionFunc(prompt, options)
	}
	return "", nil
}

func (m *MockOpenCodeClient) ContinueSession(sessionID, prompt string, options *clients.OpenCodeOptions) (string, error) {
	if m.ContinueSessionFunc != nil {
		return m.ContinueSessionFunc(sessionID, prompt, options)
	}
	return "", nil
}
