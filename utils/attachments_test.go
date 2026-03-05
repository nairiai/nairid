package utils

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nairid/clients"
)

// Test magic bytes detection for various file types

func TestDetermineFileExtensionFromMagicBytes_PNG(t *testing.T) {
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	ext := DetermineFileExtensionFromMagicBytes(pngHeader)
	if ext != ".png" {
		t.Errorf("Expected .png, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_JPEG(t *testing.T) {
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	ext := DetermineFileExtensionFromMagicBytes(jpegHeader)
	if ext != ".jpg" {
		t.Errorf("Expected .jpg, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_GIF87a(t *testing.T) {
	gifHeader := []byte("GIF87a")
	ext := DetermineFileExtensionFromMagicBytes(gifHeader)
	if ext != ".gif" {
		t.Errorf("Expected .gif, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_GIF89a(t *testing.T) {
	gifHeader := []byte("GIF89a")
	ext := DetermineFileExtensionFromMagicBytes(gifHeader)
	if ext != ".gif" {
		t.Errorf("Expected .gif, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_PDF(t *testing.T) {
	pdfHeader := []byte("%PDF-1.4\n")
	ext := DetermineFileExtensionFromMagicBytes(pdfHeader)
	if ext != ".pdf" {
		t.Errorf("Expected .pdf, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_ZIP(t *testing.T) {
	zipHeader := []byte{0x50, 0x4B, 0x03, 0x04, 0x14, 0x00}
	ext := DetermineFileExtensionFromMagicBytes(zipHeader)
	if ext != ".zip" {
		t.Errorf("Expected .zip, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_BMP(t *testing.T) {
	bmpHeader := []byte{0x42, 0x4D, 0x00, 0x00}
	ext := DetermineFileExtensionFromMagicBytes(bmpHeader)
	if ext != ".bmp" {
		t.Errorf("Expected .bmp, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_ICO(t *testing.T) {
	icoHeader := []byte{0x00, 0x00, 0x01, 0x00}
	ext := DetermineFileExtensionFromMagicBytes(icoHeader)
	if ext != ".ico" {
		t.Errorf("Expected .ico, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_GZIP(t *testing.T) {
	gzipHeader := []byte{0x1F, 0x8B, 0x08, 0x00}
	ext := DetermineFileExtensionFromMagicBytes(gzipHeader)
	if ext != ".gz" {
		t.Errorf("Expected .gz, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_RAR(t *testing.T) {
	rarHeader := []byte{0x52, 0x61, 0x72, 0x21, 0x1A, 0x07}
	ext := DetermineFileExtensionFromMagicBytes(rarHeader)
	if ext != ".rar" {
		t.Errorf("Expected .rar, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_7Z(t *testing.T) {
	sevenZHeader := []byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}
	ext := DetermineFileExtensionFromMagicBytes(sevenZHeader)
	if ext != ".7z" {
		t.Errorf("Expected .7z, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_BZ2(t *testing.T) {
	bz2Header := []byte{0x42, 0x5A, 0x68, 0x39}
	ext := DetermineFileExtensionFromMagicBytes(bz2Header)
	if ext != ".bz2" {
		t.Errorf("Expected .bz2, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_WebP(t *testing.T) {
	webpHeader := []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50}
	ext := DetermineFileExtensionFromMagicBytes(webpHeader)
	if ext != ".webp" {
		t.Errorf("Expected .webp, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_OldMSOffice(t *testing.T) {
	docHeader := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1}
	ext := DetermineFileExtensionFromMagicBytes(docHeader)
	if ext != ".doc" {
		t.Errorf("Expected .doc, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_ShellScript(t *testing.T) {
	shellHeader := []byte("#!/bin/bash\n")
	ext := DetermineFileExtensionFromMagicBytes(shellHeader)
	if ext != ".sh" {
		t.Errorf("Expected .sh, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_PHP(t *testing.T) {
	phpHeader := []byte("<?php\necho 'test';")
	ext := DetermineFileExtensionFromMagicBytes(phpHeader)
	if ext != ".php" {
		t.Errorf("Expected .php, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_XML(t *testing.T) {
	xmlHeader := []byte("<?xml version=\"1.0\"?>")
	ext := DetermineFileExtensionFromMagicBytes(xmlHeader)
	if ext != ".xml" {
		t.Errorf("Expected .xml, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_PlainText(t *testing.T) {
	textContent := []byte("This is a plain text file with some content.\nIt has multiple lines.\n")
	ext := DetermineFileExtensionFromMagicBytes(textContent)
	if ext != ".txt" {
		t.Errorf("Expected .txt, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_Unknown(t *testing.T) {
	unknownHeader := []byte{0x00, 0xFF, 0xAA, 0xBB, 0xCC, 0xDD}
	ext := DetermineFileExtensionFromMagicBytes(unknownHeader)
	if ext != ".bin" {
		t.Errorf("Expected .bin, got %s", ext)
	}
}

func TestDetermineFileExtensionFromMagicBytes_Empty(t *testing.T) {
	emptyContent := []byte{}
	ext := DetermineFileExtensionFromMagicBytes(emptyContent)
	if ext != ".bin" {
		t.Errorf("Expected .bin for empty content, got %s", ext)
	}
}

// Test attachment fetching and storage with mock API server

func TestFetchAndStoreAttachment_ValidPNG(t *testing.T) {
	// Create a PNG header as base64
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	base64Content := base64.StdEncoding.EncodeToString(pngHeader)

	// Create mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request has correct headers (Bearer token)
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-api-key" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Return mock attachment response
		response := map[string]string{
			"id":   "test-attachment-id",
			"data": base64Content,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create agents API client with mock server URL
	client := clients.NewAgentsApiClient("test-api-key", server.URL, "test-agent-id")

	sessionID := "test_session_png"
	filePath, err := FetchAndStoreAttachment(client, "test-attachment-id", sessionID, 0)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("Expected file to exist at %s", filePath)
	}

	// Verify file has .png extension
	if !strings.HasSuffix(filePath, ".png") {
		t.Errorf("Expected .png extension, got: %s", filePath)
	}

	// Verify file path contains session ID
	if !strings.Contains(filePath, sessionID) {
		t.Errorf("Expected file path to contain session ID %s, got: %s", sessionID, filePath)
	}

	// Verify file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if len(content) != len(pngHeader) {
		t.Errorf("Expected content length %d, got %d", len(pngHeader), len(content))
	}

	// Cleanup
	os.RemoveAll(filepath.Join("/tmp", "eksecd", "attachments", sessionID))
}

func TestFetchAndStoreAttachment_APIError(t *testing.T) {
	// Create mock API server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer server.Close()

	client := clients.NewAgentsApiClient("test-api-key", server.URL, "test-agent-id")

	sessionID := "test_session_error"
	_, err := FetchAndStoreAttachment(client, "nonexistent-id", sessionID, 0)

	if err == nil {
		t.Error("Expected error for API failure, got nil")
	}

	// Cleanup
	os.RemoveAll(filepath.Join("/tmp", "eksecd", "attachments", sessionID))
}

func TestFetchAndStoreAttachment_InvalidBase64(t *testing.T) {
	// Create mock API server that returns invalid base64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]string{
			"id":   "test-id",
			"data": "not-valid-base64!!!",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := clients.NewAgentsApiClient("test-api-key", server.URL, "test-agent-id")

	sessionID := "test_session_invalid"
	_, err := FetchAndStoreAttachment(client, "test-id", sessionID, 0)

	if err == nil {
		t.Error("Expected error for invalid base64, got nil")
	}

	if !strings.Contains(err.Error(), "invalid base64") {
		t.Errorf("Expected 'invalid base64' error, got: %v", err)
	}

	// Cleanup
	os.RemoveAll(filepath.Join("/tmp", "eksecd", "attachments", sessionID))
}

// Test directory functions

func TestGetAttachmentsDir_CreatesPath(t *testing.T) {
	sessionID := "test_session_dir"
	dir, err := GetAttachmentsDir(sessionID)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	expectedPath := filepath.Join("/tmp", "eksecd", "attachments", sessionID)
	if dir != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, dir)
	}

	// Cleanup
	os.RemoveAll(filepath.Join("/tmp", "eksecd", "attachments", sessionID))
}

func TestGetAttachmentsDir_CreatesDirectory(t *testing.T) {
	sessionID := "test_session_create_dir"
	dir, err := GetAttachmentsDir(sessionID)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify directory exists
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		t.Errorf("Expected directory to exist at %s", dir)
	}

	if !info.IsDir() {
		t.Errorf("Expected %s to be a directory", dir)
	}

	// Cleanup
	os.RemoveAll(filepath.Join("/tmp", "eksecd", "attachments", sessionID))
}

// Test formatting functions

func TestFormatAttachmentsText_SingleFile(t *testing.T) {
	paths := []string{"/tmp/eksecd/attachments/sess1/attachment_0.png"}
	text := FormatAttachmentsText(paths)

	expectedSubstrings := []string{
		"---",
		"Attachments:",
		"/tmp/eksecd/attachments/sess1/attachment_0.png",
	}

	for _, substr := range expectedSubstrings {
		if !strings.Contains(text, substr) {
			t.Errorf("Expected text to contain '%s', got: %s", substr, text)
		}
	}
}

func TestFormatAttachmentsText_MultipleFiles(t *testing.T) {
	paths := []string{
		"/tmp/eksecd/attachments/sess1/attachment_0.png",
		"/tmp/eksecd/attachments/sess1/attachment_1.pdf",
		"/tmp/eksecd/attachments/sess1/attachment_2.txt",
	}
	text := FormatAttachmentsText(paths)

	expectedSubstrings := []string{
		"---",
		"Attachments:",
		"/tmp/eksecd/attachments/sess1/attachment_0.png",
		"/tmp/eksecd/attachments/sess1/attachment_1.pdf",
		"/tmp/eksecd/attachments/sess1/attachment_2.txt",
	}

	for _, substr := range expectedSubstrings {
		if !strings.Contains(text, substr) {
			t.Errorf("Expected text to contain '%s', got: %s", substr, text)
		}
	}

	// Verify each path is on its own line with a bullet
	for _, path := range paths {
		expected := "- " + path
		if !strings.Contains(text, expected) {
			t.Errorf("Expected text to contain '%s'", expected)
		}
	}
}

func TestFormatAttachmentsText_EmptyList(t *testing.T) {
	paths := []string{}
	text := FormatAttachmentsText(paths)

	if text != "" {
		t.Errorf("Expected empty string for empty paths, got: %s", text)
	}
}

func TestFormatAttachmentsText_NilList(t *testing.T) {
	var paths []string
	text := FormatAttachmentsText(paths)

	if text != "" {
		t.Errorf("Expected empty string for nil paths, got: %s", text)
	}
}

// Test home directory expansion

func TestExpandHomeDir_WithTilde(t *testing.T) {
	path := "~/.config/eksecd/rules/test.md"
	expanded, err := ExpandHomeDir(path)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should not contain ~ anymore
	if strings.Contains(expanded, "~") {
		t.Errorf("Expected ~ to be expanded, got: %s", expanded)
	}

	// Should end with the relative part
	if !strings.HasSuffix(expanded, ".config/eksecd/rules/test.md") {
		t.Errorf("Expected path to end with .config/eksecd/rules/test.md, got: %s", expanded)
	}
}

func TestExpandHomeDir_WithoutTilde(t *testing.T) {
	path := "/absolute/path/to/file.md"
	expanded, err := ExpandHomeDir(path)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should be unchanged
	if expanded != path {
		t.Errorf("Expected path to be unchanged, got: %s", expanded)
	}
}

func TestExpandHomeDir_TildeOnly(t *testing.T) {
	path := "~"
	expanded, err := ExpandHomeDir(path)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should be the home directory
	homeDir, _ := os.UserHomeDir()
	if expanded != homeDir {
		t.Errorf("Expected home directory %s, got: %s", homeDir, expanded)
	}
}

// Test artifact fetching and storage

func TestFetchAndStoreArtifact_Success(t *testing.T) {
	// Create mock API server
	markdownContent := "# Test Artifact\nThis is a test rule."
	base64Content := base64.StdEncoding.EncodeToString([]byte(markdownContent))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request has correct headers
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-api-key" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Return base64-encoded content in JSON format (same as FetchAttachment)
		response := map[string]string{
			"id":   "test-attachment-id",
			"data": base64Content,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create agents API client with mock server URL
	client := clients.NewAgentsApiClient("test-api-key", server.URL, "test-agent-id")

	// Create temp directory for test
	tempDir := filepath.Join(os.TempDir(), "eksec_test_artifacts")
	defer os.RemoveAll(tempDir)

	location := filepath.Join(tempDir, "test-rule.md")

	// Fetch and store artifact
	err := FetchAndStoreArtifact(client, "test-attachment-id", location)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(location); os.IsNotExist(err) {
		t.Errorf("Expected file to exist at %s", location)
	}

	// Verify file content
	content, err := os.ReadFile(location)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(content) != markdownContent {
		t.Errorf("Expected content '%s', got '%s'", markdownContent, string(content))
	}
}

func TestFetchAndStoreArtifact_WithTilde(t *testing.T) {
	// Create mock API server
	markdownContent := "# Test Artifact with Tilde\nThis is a test."
	base64Content := base64.StdEncoding.EncodeToString([]byte(markdownContent))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]string{
			"id":   "test-attachment-id",
			"data": base64Content,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create agents API client
	client := clients.NewAgentsApiClient("test-api-key", server.URL, "test-agent-id")

	// Use ~ in path
	homeDir, _ := os.UserHomeDir()
	location := "~/eksec_test_artifact.md"

	// Fetch and store artifact
	err := FetchAndStoreArtifact(client, "test-attachment-id", location)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify file exists at expanded path
	expandedPath := filepath.Join(homeDir, "eksec_test_artifact.md")
	if _, err := os.Stat(expandedPath); os.IsNotExist(err) {
		t.Errorf("Expected file to exist at %s", expandedPath)
	}

	// Cleanup
	os.Remove(expandedPath)
}

func TestFetchAndStoreArtifact_APIError(t *testing.T) {
	// Create mock API server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer server.Close()

	client := clients.NewAgentsApiClient("test-api-key", server.URL, "test-agent-id")

	tempDir := filepath.Join(os.TempDir(), "eksec_test_artifacts_error")
	defer os.RemoveAll(tempDir)

	location := filepath.Join(tempDir, "test-rule.md")

	err := FetchAndStoreArtifact(client, "nonexistent-id", location)

	if err == nil {
		t.Error("Expected error for API failure, got nil")
	}
}

func TestFetchAndStoreArtifact_EmptyContent(t *testing.T) {
	// Create mock API server that returns empty base64 data
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]string{
			"id":   "test-id",
			"data": "", // Empty base64 data
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := clients.NewAgentsApiClient("test-api-key", server.URL, "test-agent-id")

	tempDir := filepath.Join(os.TempDir(), "eksec_test_artifacts_empty")
	defer os.RemoveAll(tempDir)

	location := filepath.Join(tempDir, "test-rule.md")

	err := FetchAndStoreArtifact(client, "test-id", location)

	if err == nil {
		t.Error("Expected error for empty content, got nil")
	}

	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("Expected 'empty' error, got: %v", err)
	}
}
