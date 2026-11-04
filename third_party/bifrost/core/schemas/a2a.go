// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type BifrostA2AResponseExtraFields struct {
	A2ARequestType A2ARequestType `json:"a2a_request_type"`
	AgentName      string         `json:"agent_name"`
	Latency        int64          `json:"latency"`

	UpstreamLatency *int64 `json:"upstream_latency,omitempty"`

	OverheadLatency *int64 `json:"-"`
}
type A2ARequestType string
type BifrostA2APushResponse struct {
	PushConfigID string
	DeliveryID   string
	AttemptID    string
}
type BifrostA2ASendMessageResponse struct {
	TaskID    string
	ContextID string
	MessageID string
	State     string
}
type BifrostA2AListTasksResponse struct {
	Count int
}
type BifrostA2ATaskResponse struct {
	TaskID    string
	ContextID string
	State     string
}
type BifrostA2AResponse struct {
	*BifrostA2AGetAgentCardResponse
	*BifrostA2ASendMessageResponse
	*BifrostA2ATaskResponse
	*BifrostA2AListTasksResponse
	*BifrostA2APushResponse

	ResponseBody *string
	ExtraFields  BifrostA2AResponseExtraFields
}
type BifrostA2AGetAgentCardResponse struct{}

const (
	A2ARequestTypeGetAgentCard A2ARequestType = "get_agent_card"

	A2ARequestTypeGetExtendedAgentCard A2ARequestType = "get_extended_agent_card"

	A2ARequestTypeSendMessage A2ARequestType = "send_message"

	A2ARequestTypeSendStreamingMessage A2ARequestType = "send_streaming_message"

	A2ARequestTypeGetTask A2ARequestType = "get_task"

	A2ARequestTypeListTasks A2ARequestType = "list_tasks"

	A2ARequestTypeCancelTask A2ARequestType = "cancel_task"

	A2ARequestTypeSubscribeToTask A2ARequestType = "subscribe_to_task"

	A2ARequestTypeCreateTaskPushConfig A2ARequestType = "create_task_push_config"

	A2ARequestTypeGetTaskPushConfig A2ARequestType = "get_task_push_config"

	A2ARequestTypeListTaskPushConfigs A2ARequestType = "list_task_push_configs"

	A2ARequestTypeDeleteTaskPushConfig A2ARequestType = "delete_task_push_config"

	A2ARequestTypePushNotification A2ARequestType = "push_notification"

	A2ARequestTypePushDelivery A2ARequestType = "push_delivery"
)

func (t A2ARequestType) IsStreaming() bool {
	switch t {
	case A2ARequestTypeSendStreamingMessage, A2ARequestTypeSubscribeToTask:
		return true
	}
	return false
}

func (t A2ARequestType) OperationName() string {
	switch t {
	case A2ARequestTypeGetAgentCard:
		return "GetAgentCard"
	case A2ARequestTypeGetExtendedAgentCard:
		return "GetExtendedAgentCard"
	case A2ARequestTypeSendMessage:
		return "SendMessage"
	case A2ARequestTypeSendStreamingMessage:
		return "SendStreamingMessage"
	case A2ARequestTypeGetTask:
		return "GetTask"
	case A2ARequestTypeListTasks:
		return "ListTasks"
	case A2ARequestTypeCancelTask:
		return "CancelTask"
	case A2ARequestTypeSubscribeToTask:
		return "SubscribeToTask"
	case A2ARequestTypeCreateTaskPushConfig:
		return "CreateTaskPushNotificationConfig"
	case A2ARequestTypeGetTaskPushConfig:
		return "GetTaskPushNotificationConfig"
	case A2ARequestTypeListTaskPushConfigs:
		return "ListTaskPushNotificationConfigs"
	case A2ARequestTypeDeleteTaskPushConfig:
		return "DeleteTaskPushNotificationConfig"
	default:
		return string(t)
	}
}

func (r *BifrostA2AResponse) PopulateExtraFields(a2aRequestType A2ARequestType, agentName string) {
	if r == nil {
		return
	}
	if r.ExtraFields.A2ARequestType == "" {
		r.ExtraFields.A2ARequestType = a2aRequestType
	}
	if r.ExtraFields.AgentName == "" {
		r.ExtraFields.AgentName = agentName
	}
}
