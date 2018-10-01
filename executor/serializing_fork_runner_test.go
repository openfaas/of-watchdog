package executor

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type mockWriter struct {
	Error error
}

func (m mockWriter) Write(p []byte) (int, error) {
	return len(p), m.Error
}

func (m mockWriter) Close() error {
	return nil
}

type mockReader struct {
	BytesRead []byte
	Error     error
}

func (m mockReader) Read(p []byte) (int, error) {
	copy(p, m.BytesRead)
	return len(m.BytesRead), m.Error
}

func Test_PipeToProcess_ReturnsBothErrors(t *testing.T) {
	expectedMockWriteError := errors.New("mockWriteError")
	expectedMockReadError := errors.New("mockReadError")
	expectedErr := errors.New("mockWriteError and mockReadError")

	mockWrite := mockWriter{
		Error: expectedMockWriteError,
	}
	mockRead := mockReader{
		BytesRead: []byte(""),
		Error:     expectedMockReadError,
	}

	data := []byte("data")
	_, err := pipeToProcess(mockWrite, mockRead, &data)

	if err == nil {
		t.Fatalf("Expected non-nil error")
	}

	if err.Error() != expectedErr.Error() {
		t.Errorf("Expected error to be: %v, got: %v", expectedErr, err)
	}
}

func Test_PipeToProcess_ReturnsWriteError(t *testing.T) {
	expectedMockWriteError := errors.New("mockWriteError")

	mockWrite := mockWriter{
		Error: expectedMockWriteError,
	}
	mockRead := mockReader{
		BytesRead: []byte(""),
		Error:     io.EOF,
	}

	data := []byte("data")
	_, err := pipeToProcess(mockWrite, mockRead, &data)

	if err == nil {
		t.Fatalf("Expected non-nil error")
	}

	if err.Error() != expectedMockWriteError.Error() {
		t.Errorf("Expected error to be: %v, got: %v", expectedMockWriteError, err)
	}
}

func Test_PipeToProcess_ReturnsReadError(t *testing.T) {
	expectedMockReadError := errors.New("mockReadError")

	mockWrite := mockWriter{
		Error: nil,
	}
	mockRead := mockReader{
		BytesRead: []byte(""),
		Error:     expectedMockReadError,
	}

	data := []byte("data")
	_, err := pipeToProcess(mockWrite, mockRead, &data)

	if err == nil {
		t.Fatalf("Expected non-nil error")
	}

	if err.Error() != expectedMockReadError.Error() {
		t.Errorf("Expected error to be: %v, got: %v", expectedMockReadError, err)
	}
}

func Test_PipeToProcess_FunctionResult(t *testing.T) {
	expectedFunctionResult := []byte("function result")

	mockWrite := mockWriter{
		Error: nil,
	}
	mockRead := mockReader{
		BytesRead: expectedFunctionResult,
		Error:     io.EOF,
	}

	data := []byte("data")
	functionResult, err := pipeToProcess(mockWrite, mockRead, &data)

	if err != nil {
		t.Fatalf("Expected nil error")
	}

	if functionResult == nil {
		t.Fatalf("functionResult is nil")
	}

	if !bytes.Equal(*functionResult, expectedFunctionResult) {
		t.Errorf("functionResult is incorrect - got: %s expected: %s", string(*functionResult), string(expectedFunctionResult))
	}

}
