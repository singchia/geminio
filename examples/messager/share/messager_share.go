package share

import (
	"context"
	"strconv"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
)

func Receive(end geminio.End) {
	for {
		msg, err := end.Receive(context.TODO())
		if err != nil {
			log.Errorf("receive err: %s", err)
			return
		}
		msg.Done()
		log.Info("> ", msg.ClientID(), msg.StreamID(), msg.Data())
	}
}

func Publish(end geminio.End, count int) error {
	for i := 0; i < count; i++ {
		msg := end.NewMessage([]byte(strconv.Itoa(i)))
		err := end.Publish(context.TODO(), msg)
		if err != nil {
			log.Errorf("publish err: %s", err)
			return err
		}
	}
	return nil
}
