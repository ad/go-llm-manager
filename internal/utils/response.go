package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type ErrorResponse struct {
	Error string `json:"error"`
}

type SuccessResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
}

// Enhanced error handling with detailed error codes and logging

type DetailedError struct {
	Error     string                 `json:"error"`
	Code      string                 `json:"code"`
	Details   string                 `json:"details,omitempty"`
	Context   map[string]interface{} `json:"context,omitempty"`
	RequestID string                 `json:"request_id,omitempty"`
	Timestamp int64                  `json:"timestamp"`
}

// Error codes for better error categorization
const (
	ErrorCodeValidation     = "VALIDATION_ERROR"
	ErrorCodeAuthentication = "AUTH_ERROR"
	ErrorCodeAuthorization  = "AUTHZ_ERROR"
	ErrorCodeRateLimit      = "RATE_LIMIT_ERROR"
	ErrorCodeDatabase       = "DATABASE_ERROR"
	ErrorCodeInternal       = "INTERNAL_ERROR"
	ErrorCodeNotFound       = "NOT_FOUND"
	ErrorCodeConflict       = "CONFLICT"
	ErrorCodeTimeout        = "TIMEOUT"
	ErrorCodeServiceBusy    = "SERVICE_BUSY"
)

// SendError sends a JSON error response
func SendError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

// SendSuccess sends a JSON success response
func SendSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SuccessResponse{Success: true, Data: data})
}

// SendSuccessWithStatus sends a successful response with custom status code
func SendSuccessWithStatus(w http.ResponseWriter, statusCode int, data interface{}) {
	SendJSON(w, statusCode, map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// SendJSON sends a JSON response with custom status code
func SendJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// ParseJSON parses JSON from request body
func ParseJSON(r *http.Request, target interface{}) error {
	return json.NewDecoder(r.Body).Decode(target)
}

// JSONString converts interface{} to JSON string
func JSONString(data interface{}) (string, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// SendDetailedError sends a detailed JSON error response with structured logging
func SendDetailedError(w http.ResponseWriter, statusCode int, code, message, details string, context map[string]interface{}) {
	errorResp := DetailedError{
		Error:     message,
		Code:      code,
		Details:   details,
		Context:   context,
		Timestamp: time.Now().UnixMilli(),
	}

	// Structured logging of the error
	logFields := map[string]interface{}{
		"error_code":    code,
		"error_message": message,
		"status_code":   statusCode,
		"timestamp":     errorResp.Timestamp,
	}

	if details != "" {
		logFields["details"] = details
	}

	if context != nil {
		logFields["context"] = context
	}

	// Log the error (in production, you'd use a proper logging library like logrus or zap)
	fmt.Printf("[ERROR] %+v\n", logFields)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(errorResp)
}

// LogAndSendError logs error details and sends a simplified error response
func LogAndSendError(w http.ResponseWriter, statusCode int, code, publicMessage, internalDetails string, err error) {
	context := make(map[string]interface{})

	if err != nil {
		context["internal_error"] = err.Error()
	}

	// Log internal details but send only public message to client
	logFields := map[string]interface{}{
		"error_code":       code,
		"public_message":   publicMessage,
		"internal_details": internalDetails,
		"status_code":      statusCode,
		"timestamp":        time.Now().UnixMilli(),
	}

	if err != nil {
		logFields["underlying_error"] = err.Error()
	}

	fmt.Printf("[ERROR] %+v\n", logFields)

	// Send only public message to client
	SendError(w, statusCode, publicMessage)
}

// SendValidationError sends a validation error with field details
func SendValidationError(w http.ResponseWriter, message string, fieldErrors map[string]string) {
	context := map[string]interface{}{}
	if fieldErrors != nil {
		context["field_errors"] = fieldErrors
	}

	SendDetailedError(w, http.StatusBadRequest, ErrorCodeValidation, message, "Input validation failed", context)
}

// SendDatabaseError sends a database error with safe public message
func SendDatabaseError(w http.ResponseWriter, err error, operation string) {
	LogAndSendError(w, http.StatusInternalServerError, ErrorCodeDatabase,
		"Internal server error", fmt.Sprintf("Database operation failed: %s", operation), err)
}
