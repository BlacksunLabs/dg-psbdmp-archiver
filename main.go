package main

// Copyright 2019 Blacksun Research Labs

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

// 	http://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/BlacksunLabs/drgero/event"
	"github.com/BlacksunLabs/drgero/mq"
	"gopkg.in/mgo.v2"
)

// Paste holds the ID and raw paste data of a paste
type Paste struct {
	ID   string `bson:"paste_id" json:"paste_id"`
	Data string
}

var m = new(mq.Client)
var db *mgo.Database
var mongoHost string

func pasteModelInxed() mgo.Index {
	return mgo.Index{
		Key:        []string{"paste_id"},
		Unique:     true,
		DropDups:   true,
		Background: true,
		Sparse:     true,
	}
}

func (p *Paste) getPasteData() (err error) {
	url := fmt.Sprintf("https://pastebin.com/raw/%s", p.ID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Add("cache-control", "no-cache")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}

	p.Data = string(body)
	return nil
}

func main() {
	mongoHost := os.Getenv("MONGO_HOST")

	session, err := mgo.Dial(mongoHost)
	if err != nil {
		log.Printf("failed to connect to MongoDB Host: %v", err)
		return
	}

	db = session.DB("pastes")

	err = m.Connect("amqp://guest:guest@localhost:5672")
	if err != nil {
		log.Printf("unable to connect to RabbitMQ : %v", err)
	}

	queueName, err := m.NewTempQueue()
	if err != nil {
		log.Printf("could not create temporary queue : %v", err)
	}

	err = m.BindQueueToExchange(queueName, "events")
	if err != nil {
		log.Printf("%v", err)
		return
	}

	ch, err := m.GetChannel()
	if err != nil {
		log.Printf("%v", err)
		return
	}

	events, err := ch.Consume(
		queueName,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("failed to register consumer to %s : %v", queueName, err)
		return
	}

	forever := make(chan bool)

	go func() {
		for e := range events {
			var event = new(event.Event)
			var err = json.Unmarshal(e.Body, event)
			if err != nil {
				log.Printf("failed to unmarshal event: %v", err)
				<-forever
			}

			if event.UserAgent != "psbmon" {
				break
			}

			var p = &Paste{}
			p.ID = event.Message[1 : len(event.Message)-1]

			err = p.getPasteData()
			if err != nil {
				log.Printf("%v", err)
			}

			err = db.C("pastes").Insert(*p)
			if err != nil {
				log.Printf("error inserting paste: %v : %v", *p, err)
				break
			}
			log.Printf("Saved paste `%s`", p.ID)
		}
	}()

	log.Println("[i] Waiting for events. To exit press CTRL+C")
	<-forever
}
