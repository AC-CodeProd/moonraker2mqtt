//go:build natiu && !tinygo

package mqtt

import (
	"fmt"
	"testing"
)

// Topic churn uses controlled same-boot retirement; a healthy socket is not
// permission to retain an unlimited official Client subscription history.
func TestSubscriptionHistoryBoundAcrossTopicChurn(t *testing.T) {
	c, ctx := budgetClient(t, 0)
	handler := func(string, []byte) {}
	subscribe := func(topic string) {
		t.Helper()
		if err := c.Subscribe(topic, handler); err != nil {
			if c.IsConnected() {
				t.Fatal(err)
			}
			if err = c.ConnectContext(ctx); err != nil {
				t.Fatal(err)
			}
			if err = c.Subscribe(topic, handler); err != nil {
				t.Fatal(err)
			}
		}
		client, _ := c.snapshot()
		if len(client.SubscribedTopics()) > subscriptionBudget {
			t.Fatal("native history unbounded")
		}
	}
	for i := 0; i < 512; i++ {
		topic := fmt.Sprintf("offline/topic/%d", i)
		subscribe(topic)
		subscribe(topic)
		if err := c.Unsubscribe(topic); err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		n := len(c.handlers)
		c.mu.Unlock()
		if n != 0 {
			t.Fatal("unsubscribe did not release handler")
		}
	}
	for i := 0; i < maxSubscriptions; i++ {
		subscribe(fmt.Sprintf("offline/keep/%d", i))
	}
	if err := c.Subscribe("offline/fifth", handler); err == nil {
		t.Fatal("four-handler capacity lost")
	}
	if c.lifecycle.generation <= 1 || c.lifecycle.starts != 3 {
		t.Fatal("fresh generations/reusable workers not reached")
	}
	t.Log("512 unique topics, accepted repeats, unsubscribe churn, four handlers; bounded history and same-boot fresh native clients with three reusable workers")
}
