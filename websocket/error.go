package websocket

import "fmt"

type (
	ClientNotConnectedError struct {
		message string
	}

	ClientAlreadyConnectedError struct {
		message string
	}

	ClientNotAuthenticatedError struct {
		message string
	}

	RequestTimeoutError struct {
		message string
		timeout int
	}

	WebSocketError struct {
		message string
		err     error
	}
)

func (e *ClientNotConnectedError) Error() string {
	if e.message != "" {
		return e.message
	}
	return "client not connected to server"
}

func (e *ClientAlreadyConnectedError) Error() string {
	if e.message != "" {
		return e.message
	}
	return "client already connected to server"
}

func (e *ClientNotAuthenticatedError) Error() string {
	if e.message != "" {
		return e.message
	}
	return "client not authenticated with server"
}

func (e *RequestTimeoutError) Error() string {
	if e.message != "" {
		return e.message
	}
	return fmt.Sprintf("request timed out after %d seconds", e.timeout)
}

func (e *WebSocketError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("websocket error: %s - %v", e.message, e.err)
	}
	return fmt.Sprintf("websocket error: %s", e.message)
}

func NewClientNotConnectedError(message string) *ClientNotConnectedError {
	return &ClientNotConnectedError{message: message}
}

func NewClientAlreadyConnectedError(message string) *ClientAlreadyConnectedError {
	return &ClientAlreadyConnectedError{message: message}
}

func NewClientNotAuthenticatedError(message string) *ClientNotAuthenticatedError {
	return &ClientNotAuthenticatedError{message: message}
}

func NewRequestTimeoutError(message string, timeout int) *RequestTimeoutError {
	return &RequestTimeoutError{message: message, timeout: timeout}
}

func NewWebSocketError(message string, err error) *WebSocketError {
	return &WebSocketError{message: message, err: err}
}

func NewWebSocketNotConnectedError(message string) error {
	return &ClientNotConnectedError{message: message}
}

func NewWebSocketTimeoutError(message string) error {
	return &RequestTimeoutError{message: message, timeout: 30}
}
