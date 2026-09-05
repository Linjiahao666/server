package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// APIError is the unified error response body.
type APIError struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains machine-readable and human-readable error info.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteError writes a JSON error response.
func WriteError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, APIError{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(c *gin.Context, status int, body interface{}) {
	c.JSON(status, body)
}

// NoContent writes an empty response with the given status code.
func NoContent(c *gin.Context, status int) {
	c.Status(status)
}

// StatusUnauthorized is HTTP 401.
const StatusUnauthorized = http.StatusUnauthorized

// StatusConflict is HTTP 409.
const StatusConflict = http.StatusConflict

// StatusUnprocessableEntity is HTTP 422.
const StatusUnprocessableEntity = http.StatusUnprocessableEntity

// StatusCreated is HTTP 201.
const StatusCreated = http.StatusCreated

// StatusNoContent is HTTP 204.
const StatusNoContent = http.StatusNoContent

// StatusBadRequest is HTTP 400.
const StatusBadRequest = http.StatusBadRequest

// StatusForbidden is HTTP 403.
const StatusForbidden = http.StatusForbidden

// StatusNotFound is HTTP 404.
const StatusNotFound = http.StatusNotFound

// StatusPayloadTooLarge is HTTP 413.
const StatusPayloadTooLarge = http.StatusRequestEntityTooLarge
