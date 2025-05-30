package models

// AuthConfig holds authentication-related configuration for nodes.
type AuthConfig struct {
	PublicKey  string `json:"publicKey"`  // Hex-encoded RSA public key
	SharedSecret string `json:"sharedSecret"` // Optional shared secret for Secret Mode
}