package kafka

// Topic names. Kept as constants to avoid typo-based routing bugs.
const (
	TopicOrderPlaced         = "order.placed"
	TopicOrderAssigned       = "order.assigned"
	TopicOrderStatusUpdated  = "order.status.updated"
	TopicRiderLocationUpdate = "rider.location.updated"
	TopicETAUpdated          = "eta.updated"
)

// AllTopics is the full set of topics in the system. Services pass this to
// EnsureTopics at boot so startup order doesn't matter.
var AllTopics = []string{
	TopicOrderPlaced,
	TopicOrderAssigned,
	TopicOrderStatusUpdated,
	TopicRiderLocationUpdate,
	TopicETAUpdated,
}
