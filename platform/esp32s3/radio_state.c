//go:build tinygo && esp32s3

/* Pinned espradio has no public association getter. Observe its existing event
 * setter through GNU ld --wrap; never alter/replace its original behavior.
 * This is not an ISR callback and carries no SSID/password/event payload.
 */
#include <stdint.h>
static volatile uint32_t bridge_associated;
void __real_espradio_netif_set_connected(int connected);
void __wrap_espradio_netif_set_connected(int connected) {
    bridge_associated = connected != 0;
    __real_espradio_netif_set_connected(connected);
}
int bridge_radio_associated(void) { return bridge_associated != 0; }
