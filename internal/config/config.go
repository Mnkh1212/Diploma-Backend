package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	JWTSecret  string
	Port       string
	AIAPIKey   string
	AIModel    string
	GoogleIOSClientID string
	GoogleWebClientID string
	FacebookAppID     string
	AppleBundleID     string
}

func Load() *Config {
	godotenv.Load()

	return &Config{
		DBHost:     getEnv("DB_HOST", DefaultDBHost),
		DBPort:     getEnv("DB_PORT", DefaultDBPort),
		DBUser:     getEnv("DB_USER", "fintrack"),
		DBPassword: getEnv("DB_PASSWORD", "fintrack123"),
		DBName:     getEnv("DB_NAME", "fintrack"),
		JWTSecret:  getEnv("JWT_SECRET", "fintrack-secret-key-change-in-production"),
		Port:       getEnv("PORT", DefaultPort),
		AIAPIKey:   getFirstEnv("AI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"),
		AIModel:    getEnv("AI_MODEL", "gemini-2.0-flash"),
		GoogleIOSClientID: getEnv("GOOGLE_IOS_CLIENT_ID", ""),
		GoogleWebClientID: getEnv("GOOGLE_WEB_CLIENT_ID", ""),
		FacebookAppID:     getEnv("FACEBOOK_APP_ID", ""),
		AppleBundleID:     getEnv("APPLE_BUNDLE_ID", ""),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getFirstEnv(keys ...string) string {
	for _, key := range keys {
		if value, exists := os.LookupEnv(key); exists && value != "" {
			return value
		}
	}
	return ""
}
