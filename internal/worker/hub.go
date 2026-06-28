package worker

type Event struct {
	Type      string `json:"type"`
	TaskID    string `json:"task_id"`
	ChannelID string `json:"channel_id"`
	SessionID string `json:"session_id,omitempty"`
	Status    Status `json:"status"`
	Progress  int    `json:"progress"`
	Message   string `json:"message"`
	Error     string `json:"error,omitempty"`
}

type Hub struct {
	subscribe   chan chan Event
	unsubscribe chan chan Event
	broadcast   chan Event
	done        chan struct{}
}

func NewHub() *Hub {
	return &Hub{
		subscribe:   make(chan chan Event),
		unsubscribe: make(chan chan Event),
		broadcast:   make(chan Event, 64),
		done:        make(chan struct{}),
	}
}

func (h *Hub) Run() {
	subscribers := map[chan Event]struct{}{}
	for {
		select {
		case subscriber := <-h.subscribe:
			subscribers[subscriber] = struct{}{}
		case subscriber := <-h.unsubscribe:
			if _, ok := subscribers[subscriber]; ok {
				delete(subscribers, subscriber)
				close(subscriber)
			}
		case event := <-h.broadcast:
			for subscriber := range subscribers {
				select {
				case subscriber <- event:
				default:
				}
			}
		case <-h.done:
			for subscriber := range subscribers {
				close(subscriber)
			}
			return
		}
	}
}

func (h *Hub) Subscribe() chan Event {
	subscriber := make(chan Event, 16)
	h.subscribe <- subscriber
	return subscriber
}

func (h *Hub) Unsubscribe(subscriber chan Event) {
	h.unsubscribe <- subscriber
}

func (h *Hub) Broadcast(task Task) {
	h.broadcast <- Event{
		Type:      "task_progress",
		TaskID:    task.ID,
		ChannelID: task.ChannelID,
		SessionID: task.SessionID,
		Status:    task.Status,
		Progress:  task.Progress,
		Message:   task.Message,
		Error:     task.Error,
	}
}

func (h *Hub) Stop() {
	close(h.done)
}
