package protocol

const (
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

type Result struct {
	Type            string        `json:"type"`
	ProtocolVersion int           `json:"protocolVersion"`
	OperationID     *string       `json:"operationId"`
	OK              bool          `json:"ok"`
	Status          string        `json:"status"`
	Data            any           `json:"data"`
	Error           *ProductError `json:"error"`
}

type ProductError struct {
	Code       string  `json:"code"`
	IncidentID *string `json:"incidentId"`
	Retryable  bool    `json:"retryable"`
	Action     string  `json:"action"`
}

func Success(data any) Result {
	return Result{
		Type:            "result",
		ProtocolVersion: 1,
		OK:              true,
		Status:          StatusCompleted,
		Data:            data,
	}
}

func Failure(code, action string, retryable bool) Result {
	return Result{
		Type:            "result",
		ProtocolVersion: 1,
		OK:              false,
		Status:          StatusFailed,
		Error: &ProductError{
			Code:      code,
			Retryable: retryable,
			Action:    action,
		},
	}
}
