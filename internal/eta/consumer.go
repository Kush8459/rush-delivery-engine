package eta

import (
	"context"
	"encoding/json"

	"github.com/Kush8459/rush-delivery-engine/internal/location"
	"github.com/rs/zerolog"
	"github.com/segmentio/kafka-go"
)

func Handler(svc *Service, log zerolog.Logger) func(ctx context.Context, msg kafka.Message) error {
	return func(ctx context.Context, msg kafka.Message) error {
		var evt location.RiderLocationUpdatedEvent
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Error().Err(err).Msg("bad rider.location.updated payload; skipping")
			return nil
		}
		return svc.OnLocation(ctx, evt)
	}
}
