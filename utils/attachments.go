package utils

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"nairid/clients"
	"nairid/core/env"
	"nairid/core/log"
)

// DetermineFileExtensionFromMagicBytes inspects the first few bytes of content
// to determine the file type and returns the appropriate file extension
func DetermineFileExtensionFromMagicBytes(content []byte) string {
	if len(content) == 0 {
		return ".bin"
	}

	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if len(content) >= 8 && bytes.Equal(content[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return ".png"
	}

	// JPEG: FF D8 FF
	if len(content) >= 3 && bytes.Equal(content[:3], []byte{0xFF, 0xD8, 0xFF}) {
		return ".jpg"
	}

	// GIF: GIF87a or GIF89a
	if len(content) >= 6 {
		gifHeader := string(content[:6])
		if gifHeader == "GIF87a" || gifHeader == "GIF89a" {
			return ".gif"
		}
	}

	// PDF: %PDF
	if len(content) >= 4 && string(content[:4]) == "%PDF" {
		return ".pdf"
	}

	// ZIP: 50 4B 03 04 (also used by docx, xlsx, etc.)
	if len(content) >= 4 && bytes.Equal(content[:4], []byte{0x50, 0x4B, 0x03, 0x04}) {
		return ".zip"
	}

	// BMP: 42 4D
	if len(content) >= 2 && bytes.Equal(content[:2], []byte{0x42, 0x4D}) {
		return ".bmp"
	}

	// ICO: 00 00 01 00
	if len(content) >= 4 && bytes.Equal(content[:4], []byte{0x00, 0x00, 0x01, 0x00}) {
		return ".ico"
	}

	// GZIP: 1F 8B
	if len(content) >= 2 && bytes.Equal(content[:2], []byte{0x1F, 0x8B}) {
		return ".gz"
	}

	// RAR: 52 61 72 21
	if len(content) >= 4 && bytes.Equal(content[:4], []byte{0x52, 0x61, 0x72, 0x21}) {
		return ".rar"
	}

	// 7z: 37 7A BC AF 27 1C
	if len(content) >= 6 && bytes.Equal(content[:6], []byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}) {
		return ".7z"
	}

	// BZ2: 42 5A 68
	if len(content) >= 3 && bytes.Equal(content[:3], []byte{0x42, 0x5A, 0x68}) {
		return ".bz2"
	}

	// WebP: RIFF....WEBP
	if len(content) >= 12 && bytes.Equal(content[:4], []byte{0x52, 0x49, 0x46, 0x46}) &&
		bytes.Equal(content[8:12], []byte{0x57, 0x45, 0x42, 0x50}) {
		return ".webp"
	}

	// Old MS Office: D0 CF 11 E0
	if len(content) >= 4 && bytes.Equal(content[:4], []byte{0xD0, 0xCF, 0x11, 0xE0}) {
		return ".doc"
	}

	// Check for shell script: #!/
	if len(content) >= 2 && string(content[:2]) == "#!" {
		return ".sh"
	}

	// Check for PHP: <?php
	if len(content) >= 5 && string(content[:5]) == "<?php" {
		return ".php"
	}

	// Check for XML: <?xml
	if len(content) >= 5 && string(content[:5]) == "<?xml" {
		return ".xml"
	}

	// Check if it's plain text (valid UTF-8 with printable characters)
	if isPlainText(content) {
		return ".txt"
	}

	// Default to binary
	return ".bin"
}

// isPlainText checks if content appears to be plain text
func isPlainText(content []byte) bool {
	if !utf8.Valid(content) {
		return false
	}

	// Check first 512 bytes (or entire content if smaller)
	sample := content
	if len(content) > 512 {
		sample = content[:512]
	}

	// Count printable characters
	printableCount := 0
	for _, b := range sample {
		// Printable ASCII, tab, newline, carriage return
		if (b >= 32 && b <= 126) || b == 9 || b == 10 || b == 13 {
			printableCount++
		}
	}

	// If more than 95% are printable, consider it text
	threshold := float64(len(sample)) * 0.95
	return float64(printableCount) >= threshold
}

// GetAttachmentsDir returns the directory path for storing attachments for a given session
// and creates the directory if it doesn't exist
func GetAttachmentsDir(sessionID string) (string, error) {
	dir := filepath.Join("/tmp", "eksecd", "attachments", sessionID)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create attachments directory: %w", err)
	}

	return dir, nil
}

// FetchAndStoreAttachment fetches an attachment from the API and stores it as a binary file
// Returns the absolute path to the stored file
func FetchAndStoreAttachment(client *clients.AgentsApiClient, attachmentID string, sessionID string, index int) (string, error) {
	// Fetch attachment from API
	attachmentResp, err := client.FetchAttachment(attachmentID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch attachment %s: %w", attachmentID, err)
	}

	// Validate content is not empty
	if attachmentResp.Data == "" {
		return "", fmt.Errorf("attachment data is empty for ID %s", attachmentID)
	}

	// Decode base64 content
	content, err := base64.StdEncoding.DecodeString(attachmentResp.Data)
	if err != nil {
		return "", fmt.Errorf("invalid base64 content in attachment %s: %w", attachmentID, err)
	}

	// Check decoded content is not empty
	if len(content) == 0 {
		return "", fmt.Errorf("decoded attachment content is empty for ID %s", attachmentID)
	}

	// Determine file extension from magic bytes
	ext := DetermineFileExtensionFromMagicBytes(content)

	// Get attachments directory
	dir, err := GetAttachmentsDir(sessionID)
	if err != nil {
		return "", err
	}

	// Create filename: attachment_<index>.<ext>
	filename := fmt.Sprintf("attachment_%d%s", index, ext)
	filePath := filepath.Join(dir, filename)

	// Write binary content to file
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		return "", fmt.Errorf("failed to write attachment file: %w", err)
	}

	return filePath, nil
}

// FormatAttachmentsText formats a list of file paths into a text block
// suitable for appending to agent prompts
func FormatAttachmentsText(filePaths []string) string {
	if len(filePaths) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("Attachments:\n")
	for _, path := range filePaths {
		fmt.Fprintf(&builder, "- %s\n", path)
	}

	return builder.String()
}

// ScanOutboundAttachments lists files in the outbound attachments directory for a given job.
func ScanOutboundAttachments(jobID string) ([]string, error) {
	dir, err := env.GetOutboundAttachmentsDir(jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get outbound attachments dir: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read outbound attachments dir: %w", err)
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}

	return paths, nil
}

// UploadOutboundAttachments scans the outbound attachments directory for a job,
// uploads each file to the backend, and returns the attachment IDs.
func UploadOutboundAttachments(client *clients.AgentsApiClient, jobID string) ([]string, error) {
	files, err := ScanOutboundAttachments(jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to scan outbound attachments: %w", err)
	}

	if len(files) == 0 {
		return nil, nil
	}

	var attachmentIDs []string
	for _, filePath := range files {
		resp, err := client.UploadAttachment(filePath)
		if err != nil {
			log.Warn("\u26a0\ufe0f Failed to upload outbound attachment %s: %v", filePath, err)
			continue
		}
		attachmentIDs = append(attachmentIDs, resp.AttachmentID)
	}

	return attachmentIDs, nil
}

// ClearOutboundAttachments removes all files from the outbound attachments directory for a job.
func ClearOutboundAttachments(jobID string) error {
	dir, err := env.GetOutboundAttachmentsDir(jobID)
	if err != nil {
		return fmt.Errorf("failed to get outbound attachments dir: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read outbound attachments dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			log.Warn("\u26a0\ufe0f Failed to remove outbound attachment %s: %v", entry.Name(), err)
		}
	}

	return nil
}

// ExpandHomeDir expands the ~ character in a file path to the user's home directory
func ExpandHomeDir(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	// Replace ~ with home directory
	if path == "~" {
		return homeDir, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, path[2:]), nil
	}

	return path, nil
}

// FetchAndStoreArtifact downloads an artifact file and saves it to the specified location
// The location path may contain ~ which will be expanded to the user's home directory
func FetchAndStoreArtifact(client *clients.AgentsApiClient, attachmentID string, location string) error {
	// Expand ~ in location path
	expandedPath, err := ExpandHomeDir(location)
	if err != nil {
		return fmt.Errorf("failed to expand home directory in path %s: %w", location, err)
	}

	// Fetch attachment using existing mechanism (returns base64-encoded content)
	attachmentResp, err := client.FetchAttachment(attachmentID)
	if err != nil {
		return fmt.Errorf("failed to fetch artifact attachment %s: %w", attachmentID, err)
	}

	// Validate base64 data is not empty
	if attachmentResp.Data == "" {
		return fmt.Errorf("artifact attachment data is empty for ID %s", attachmentID)
	}

	// Decode base64 content
	content, err := base64.StdEncoding.DecodeString(attachmentResp.Data)
	if err != nil {
		return fmt.Errorf("invalid base64 content in artifact attachment %s: %w", attachmentID, err)
	}

	// Check decoded content is not empty
	if len(content) == 0 {
		return fmt.Errorf("decoded artifact content is empty for ID %s", attachmentID)
	}

	// Create parent directory if it doesn't exist
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Write content to file
	if err := os.WriteFile(expandedPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write artifact file to %s: %w", expandedPath, err)
	}

	return nil
}
