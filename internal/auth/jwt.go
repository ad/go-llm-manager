package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/ad/go-llm-manager/internal/database"
)

type JWTManager struct {
	secretKey []byte
}

func NewJWTManager(secret string) *JWTManager {
	return &JWTManager{
		secretKey: []byte(secret),
	}
}

func (j *JWTManager) GenerateToken(payload *database.JWTPayload, expiresIn int) (string, error) {
	now := time.Now()

	claims := jwt.MapClaims{
		"iss":     payload.Issuer,
		"sub":     payload.Subject,
		"exp":     now.Add(time.Duration(expiresIn) * time.Second).Unix(),
		"iat":     now.Unix(),
		"user_id": payload.UserID,
	}

	// Add aud if provided
	if payload.Audience != "" {
		claims["aud"] = payload.Audience
	}

	// Add optional fields (matching TypeScript field names exactly)
	if payload.TaskID != "" {
		claims["taskId"] = payload.TaskID
	}
	if payload.ProductData != "" {
		claims["product_data"] = payload.ProductData
	}
	if payload.Priority != nil {
		claims["priority"] = *payload.Priority
	}
	if payload.OllamaParams != nil {
		claims["ollama_params"] = payload.OllamaParams
	}
	if payload.ProcessorID != "" {
		claims["processor_id"] = payload.ProcessorID
	}
	if payload.RateLimit != nil {
		claims["rate_limit"] = payload.RateLimit
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secretKey)
}

func (j *JWTManager) VerifyToken(tokenString string) (*database.JWTPayload, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.secretKey, nil
	})

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims")
	}

	payload := &database.JWTPayload{}

	// Extract required fields
	if iss, ok := claims["iss"].(string); ok {
		payload.Issuer = iss
	}
	if aud, ok := claims["aud"].(string); ok {
		payload.Audience = aud
	}
	if sub, ok := claims["sub"].(string); ok {
		payload.Subject = sub
	}
	if userID, ok := claims["user_id"].(string); ok {
		payload.UserID = userID
	}
	if exp, ok := claims["exp"].(float64); ok {
		payload.ExpiresAt = int64(exp)
	}

	// Extract optional fields
	if taskID, ok := claims["taskId"].(string); ok {
		payload.TaskID = taskID
	}
	if productData, ok := claims["product_data"].(string); ok {
		payload.ProductData = productData
	}
	if priority, ok := claims["priority"].(float64); ok {
		priorityInt := int(priority)
		payload.Priority = &priorityInt
	}
	if processorID, ok := claims["processor_id"].(string); ok {
		payload.ProcessorID = processorID
	}

	// Handle ollama_params
	if ollamaParamsRaw, ok := claims["ollama_params"]; ok {
		if ollamaParamsMap, ok := ollamaParamsRaw.(map[string]interface{}); ok {
			ollamaParams := &database.OllamaParams{}

			if model, ok := ollamaParamsMap["model"].(string); ok {
				ollamaParams.Model = &model
			}
			if prompt, ok := ollamaParamsMap["prompt"].(string); ok {
				ollamaParams.Prompt = &prompt
			}
			if temp, ok := ollamaParamsMap["temperature"].(float64); ok {
				ollamaParams.Temperature = &temp
			}
			if maxTokens, ok := ollamaParamsMap["max_tokens"].(float64); ok {
				maxTokensInt := int(maxTokens)
				ollamaParams.MaxTokens = &maxTokensInt
			}
			if topP, ok := ollamaParamsMap["top_p"].(float64); ok {
				ollamaParams.TopP = &topP
			}
			if topK, ok := ollamaParamsMap["top_k"].(float64); ok {
				topKInt := int(topK)
				ollamaParams.TopK = &topKInt
			}
			if repeatPenalty, ok := ollamaParamsMap["repeat_penalty"].(float64); ok {
				ollamaParams.RepeatPenalty = &repeatPenalty
			}
			if seed, ok := ollamaParamsMap["seed"].(float64); ok {
				seedInt := int(seed)
				ollamaParams.Seed = &seedInt
			}
			if stopRaw, ok := ollamaParamsMap["stop"]; ok {
				if stopSlice, ok := stopRaw.([]interface{}); ok {
					stop := make([]string, len(stopSlice))
					for i, v := range stopSlice {
						if s, ok := v.(string); ok {
							stop[i] = s
						}
					}
					ollamaParams.Stop = stop
				}
			}

			payload.OllamaParams = ollamaParams
		}
	}

	// Handle rate_limit
	if rateLimitRaw, ok := claims["rate_limit"]; ok {
		if rateLimitMap, ok := rateLimitRaw.(map[string]interface{}); ok {
			rateLimit := &database.RateLimitConfig{}

			if maxRequests, ok := rateLimitMap["max_requests"].(float64); ok {
				rateLimit.MaxRequests = int(maxRequests)
			}
			if windowMs, ok := rateLimitMap["window_ms"].(float64); ok {
				rateLimit.WindowMs = int64(windowMs)
			}

			payload.RateLimit = rateLimit
		}
	}

	return payload, nil
}

func GenerateTaskID() string {
	return uuid.New().String()
}

func GenerateRandomKey(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return base64.URLEncoding.EncodeToString(bytes)
}

type JWTAuth struct {
	*JWTManager
}

func NewJWTAuth(secret string) *JWTAuth {
	return &JWTAuth{
		JWTManager: NewJWTManager(secret),
	}
}

// ExtractUserID extracts user ID from JWT token in request
func (j *JWTAuth) ExtractUserID(r *http.Request) (string, error) {
	tokenString := j.extractTokenFromRequest(r)
	if tokenString == "" {
		return "", fmt.Errorf("no token found")
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.secretKey, nil
	})

	if err != nil {
		return "", err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		if userID, exists := claims["user_id"].(string); exists {
			return userID, nil
		}
	}

	return "", fmt.Errorf("invalid token claims")
}

// ExtractPayload extracts full JWT payload from request
func (j *JWTAuth) ExtractPayload(r *http.Request) (*database.JWTPayload, error) {
	tokenString := j.extractTokenFromRequest(r)
	if tokenString == "" {
		return nil, fmt.Errorf("no token found")
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.secretKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	payload := &database.JWTPayload{}

	// Extract user_id and sub (TypeScript uses both)
	if userID, ok := claims["user_id"].(string); ok {
		payload.UserID = userID
	}
	if subject, ok := claims["sub"].(string); ok {
		payload.Subject = subject
		// If user_id is empty, use sub as fallback (matching TypeScript logic)
		if payload.UserID == "" {
			payload.UserID = subject
		}
	}
	if issuer, ok := claims["iss"].(string); ok {
		payload.Issuer = issuer
	}
	if audience, ok := claims["aud"].(string); ok {
		payload.Audience = audience
	}
	if taskID, ok := claims["taskId"].(string); ok {
		payload.TaskID = taskID
	}
	if productData, ok := claims["product_data"].(string); ok {
		payload.ProductData = productData
	}
	if priority, ok := claims["priority"].(float64); ok {
		p := int(priority)
		payload.Priority = &p
	}
	if processorID, ok := claims["processor_id"].(string); ok {
		payload.ProcessorID = processorID
	}

	// Handle ollama_params (similar to VerifyToken)
	if ollamaParamsRaw, ok := claims["ollama_params"]; ok {
		if ollamaParamsMap, ok := ollamaParamsRaw.(map[string]interface{}); ok {
			ollamaParams := &database.OllamaParams{}

			if model, ok := ollamaParamsMap["model"].(string); ok {
				ollamaParams.Model = &model
			}
			if prompt, ok := ollamaParamsMap["prompt"].(string); ok {
				ollamaParams.Prompt = &prompt
			}
			if temp, ok := ollamaParamsMap["temperature"].(float64); ok {
				ollamaParams.Temperature = &temp
			}
			if maxTokens, ok := ollamaParamsMap["max_tokens"].(float64); ok {
				maxTokensInt := int(maxTokens)
				ollamaParams.MaxTokens = &maxTokensInt
			}
			if topP, ok := ollamaParamsMap["top_p"].(float64); ok {
				ollamaParams.TopP = &topP
			}
			if topK, ok := ollamaParamsMap["top_k"].(float64); ok {
				topKInt := int(topK)
				ollamaParams.TopK = &topKInt
			}
			if repeatPenalty, ok := ollamaParamsMap["repeat_penalty"].(float64); ok {
				ollamaParams.RepeatPenalty = &repeatPenalty
			}
			if seed, ok := ollamaParamsMap["seed"].(float64); ok {
				seedInt := int(seed)
				ollamaParams.Seed = &seedInt
			}
			if stopRaw, ok := ollamaParamsMap["stop"]; ok {
				if stopSlice, ok := stopRaw.([]interface{}); ok {
					stop := make([]string, len(stopSlice))
					for i, v := range stopSlice {
						if s, ok := v.(string); ok {
							stop[i] = s
						}
					}
					ollamaParams.Stop = stop
				}
			}

			payload.OllamaParams = ollamaParams
		}
	}

	// Handle rate_limit (similar to VerifyToken)
	if rateLimitRaw, ok := claims["rate_limit"]; ok {
		if rateLimitMap, ok := rateLimitRaw.(map[string]interface{}); ok {
			rateLimit := &database.RateLimitConfig{}

			if maxRequests, ok := rateLimitMap["max_requests"].(float64); ok {
				rateLimit.MaxRequests = int(maxRequests)
			}
			if windowMs, ok := rateLimitMap["window_ms"].(float64); ok {
				rateLimit.WindowMs = int64(windowMs)
			}

			payload.RateLimit = rateLimit
		}
	}

	return payload, nil
}

// ExtractPayloadFromToken extracts JWT payload from token string
func (j *JWTAuth) ExtractPayloadFromToken(tokenString string) (*database.JWTPayload, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("empty token")
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.secretKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	payload := &database.JWTPayload{}

	// Extract user_id and sub (TypeScript uses both)
	if userID, ok := claims["user_id"].(string); ok {
		payload.UserID = userID
	}
	if subject, ok := claims["sub"].(string); ok {
		payload.Subject = subject
		// If user_id is empty, use sub as fallback (matching TypeScript logic)
		if payload.UserID == "" {
			payload.UserID = subject
		}
	}
	if issuer, ok := claims["iss"].(string); ok {
		payload.Issuer = issuer
	}
	if audience, ok := claims["aud"].(string); ok {
		payload.Audience = audience
	}
	if taskID, ok := claims["taskId"].(string); ok {
		payload.TaskID = taskID
	}
	if productData, ok := claims["product_data"].(string); ok {
		payload.ProductData = productData
	}
	if priority, ok := claims["priority"].(float64); ok {
		p := int(priority)
		payload.Priority = &p
	}
	if processorID, ok := claims["processor_id"].(string); ok {
		payload.ProcessorID = processorID
	}

	// Handle ollama_params (reusing существующей логики)
	if ollamaParamsRaw, ok := claims["ollama_params"]; ok {
		if ollamaParamsMap, ok := ollamaParamsRaw.(map[string]interface{}); ok {
			ollamaParams := &database.OllamaParams{}

			if model, ok := ollamaParamsMap["model"].(string); ok {
				ollamaParams.Model = &model
			}
			if prompt, ok := ollamaParamsMap["prompt"].(string); ok {
				ollamaParams.Prompt = &prompt
			}
			if temp, ok := ollamaParamsMap["temperature"].(float64); ok {
				ollamaParams.Temperature = &temp
			}
			if maxTokens, ok := ollamaParamsMap["max_tokens"].(float64); ok {
				maxTokensInt := int(maxTokens)
				ollamaParams.MaxTokens = &maxTokensInt
			}
			if topP, ok := ollamaParamsMap["top_p"].(float64); ok {
				ollamaParams.TopP = &topP
			}
			if topK, ok := ollamaParamsMap["top_k"].(float64); ok {
				topKInt := int(topK)
				ollamaParams.TopK = &topKInt
			}
			if repeatPenalty, ok := ollamaParamsMap["repeat_penalty"].(float64); ok {
				ollamaParams.RepeatPenalty = &repeatPenalty
			}
			if seed, ok := ollamaParamsMap["seed"].(float64); ok {
				seedInt := int(seed)
				ollamaParams.Seed = &seedInt
			}
			if stopRaw, ok := ollamaParamsMap["stop"]; ok {
				if stopSlice, ok := stopRaw.([]interface{}); ok {
					stop := make([]string, len(stopSlice))
					for i, v := range stopSlice {
						if s, ok := v.(string); ok {
							stop[i] = s
						}
					}
					ollamaParams.Stop = stop
				}
			}

			payload.OllamaParams = ollamaParams
		}
	}

	// Handle rate_limit
	if rateLimitRaw, ok := claims["rate_limit"]; ok {
		if rateLimitMap, ok := rateLimitRaw.(map[string]interface{}); ok {
			rateLimit := &database.RateLimitConfig{}

			if maxRequests, ok := rateLimitMap["max_requests"].(float64); ok {
				rateLimit.MaxRequests = int(maxRequests)
			}
			if windowMs, ok := rateLimitMap["window_ms"].(float64); ok {
				rateLimit.WindowMs = int64(windowMs)
			}

			payload.RateLimit = rateLimit
		}
	}

	return payload, nil
}

func (j *JWTAuth) extractTokenFromRequest(r *http.Request) string {
	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	// Check query parameter
	return r.URL.Query().Get("token")
}
