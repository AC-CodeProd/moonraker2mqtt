package mqtt

import "testing"

func TestSubscriptionHistoryOptOut(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "default"
		if disabled {
			name = "omitted"
		}
		t.Run(name, func(t *testing.T) {
			c := NewClient(ClientConfig{DisableSubscriptionHistory: disabled})
			c.cs.onConnect(1)
			rx, _ := c.cs.callbacks(nil)
			for i := 1; i <= 512; i++ {
				c.cs.pendingSubs = VariablesSubscribe{PacketIdentifier: 1, TopicFilters: []SubscribeRequest{{TopicFilter: []byte("same/topic"), QoS: QoS0}}}
				if err := rx.OnSuback(&c.rx, VariablesSuback{PacketIdentifier: 1, ReturnCodes: []QoSLevel{QoS0}}); err != nil {
					t.Fatal(err)
				}
				want := i
				if disabled {
					want = 0
				}
				if len(c.cs.activeSubs) != want {
					t.Fatalf("attempt=%d history=%d want=%d", i, len(c.cs.activeSubs), want)
				}
				if disabled && c.cs.activeSubs != nil {
					t.Fatal("omitted history allocated backing store")
				}
				if c.AwaitingSuback() {
					t.Fatal("pending response not cleared")
				}
			}
			// History omission must not skip existing native validation.
			c.cs.pendingSubs = VariablesSubscribe{PacketIdentifier: 2, TopicFilters: []SubscribeRequest{{TopicFilter: []byte("same/topic"), QoS: QoS0}}}
			if err := rx.OnSuback(&c.rx, VariablesSuback{PacketIdentifier: 2}); err == nil {
				t.Fatal("mismatched number of return codes accepted")
			}
			if err := rx.OnSuback(&c.rx, VariablesSuback{PacketIdentifier: 2, ReturnCodes: []QoSLevel{QoS1}}); err == nil {
				t.Fatal("wrong granted QoS accepted")
			}
			if err := rx.OnSuback(&c.rx, VariablesSuback{PacketIdentifier: 2, ReturnCodes: []QoSLevel{QoSSubfail}}); err != nil {
				t.Fatal(err)
			}
			if disabled && c.cs.activeSubs != nil {
				t.Fatal("rejection retained history")
			}
		})
	}
}
