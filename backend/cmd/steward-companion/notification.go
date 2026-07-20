package main

type systemNotificationAction struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Kind          string `json:"kind"`
	Value         string `json:"value"`
	CallbackToken string `json:"callback_token"`
}

type systemNotification struct {
	ID       string                     `json:"id"`
	Title    string                     `json:"title"`
	Body     string                     `json:"body"`
	Category string                     `json:"category"`
	Priority string                     `json:"priority"`
	Actions  []systemNotificationAction `json:"actions"`
}
