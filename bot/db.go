package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type SessionDoc struct {
	ID      string            `bson:"_id"`
	Cookies map[string]string `bson:"cookies"`
	Updated time.Time         `bson:"updated"`
}

type DBService struct {
	Client     *mongo.Client
	Collection *mongo.Collection
}

func NewDBService(uri string) (*DBService, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}

	// Kiểm tra kết nối
	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, err
	}

	col := client.Database("zalo_bot").Collection("sessions")
	return &DBService{
		Client:     client,
		Collection: col,
	}, nil
}

func (s *DBService) SaveSession(cookies map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	doc := SessionDoc{
		ID:      "current_session",
		Cookies: cookies,
		Updated: time.Now(),
	}

	opts := options.Replace().SetUpsert(true)
	_, err := s.Collection.ReplaceOne(ctx, bson.M{"_id": "current_session"}, doc, opts)
	return err
}

func (s *DBService) LoadSession() (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var doc SessionDoc
	err := s.Collection.FindOne(ctx, bson.M{"_id": "current_session"}).Decode(&doc)
	if err != nil {
		return nil, err
	}

	return doc.Cookies, nil
}

func (s *DBService) Close() {
	if s.Client != nil {
		_ = s.Client.Disconnect(context.Background())
	}
}
