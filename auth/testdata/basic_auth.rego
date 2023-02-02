package api.authz.basic

default allow = false

allow {
	input.path = "/api/private"
	valid_basic_auth
}

valid_basic_auth {
	value := trim_prefix(input.authorization, "Basic ")
	decoded := base64.decode(value)

	colon := indexof(decoded, ":")
	username := substring(decoded, 0, colon)
	password := substring(decoded, colon + 1, -1)

	bcrypt_eq(credentials[username].hash, password)
}

# generally don't store the plain value, it is only included here
# for demontration and ease of testing.
# this value would normally be loaded from a secret.
credentials := {"bob": {
	"hash": "$2y$10$/7tG7c7RQKnLki5OveBiyejtOvdR5.I3wtoAUHBUO5azpg00bbUvy",
	"plain": "secretvalue",
}}
