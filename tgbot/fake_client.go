package tgbot

import "sync"

// SentMessage records one reply the bot sent.
type SentMessage struct {
	ChatID int64
	Text   string
}

// FakeClient is an in-memory Client for tests: it returns queued updates
// (draining them) and records sent messages.
type FakeClient struct {
	mu     sync.Mutex
	queued []Update
	sent   []SentMessage
}

// NewFakeClient returns an empty FakeClient.
func NewFakeClient() *FakeClient { return &FakeClient{} }

// Queue adds updates to be returned by the next GetUpdates.
func (f *FakeClient) Queue(ups ...Update) {
	f.mu.Lock()
	f.queued = append(f.queued, ups...)
	f.mu.Unlock()
}

// GetUpdates returns and drains the queued updates.
func (f *FakeClient) GetUpdates(offset, timeoutSec int) ([]Update, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.queued
	f.queued = nil
	return out, nil
}

// SendMessage records the reply.
func (f *FakeClient) SendMessage(chatID int64, text string) error {
	f.mu.Lock()
	f.sent = append(f.sent, SentMessage{ChatID: chatID, Text: text})
	f.mu.Unlock()
	return nil
}

// Sent returns every recorded reply.
func (f *FakeClient) Sent() []SentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SentMessage(nil), f.sent...)
}
