package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

type fileTranscription struct {
	Text string `json:"text"`
}

func TranscribeFile(ctx context.Context, audioPath, language string) (string, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	f, err := os.Open(audioPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", err
	}
	w.WriteField("model", "gpt-4o-transcribe") // or "gpt-4o-mini-transcribe" (cheaper), or "whisper-1"
	w.WriteField("language", language)         // ISO-639-1
	w.WriteField("response_format", "json")
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai %d: %s", resp.StatusCode, respBody)
	}

	var tr fileTranscription
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return "", err
	}
	return tr.Text, nil
}
