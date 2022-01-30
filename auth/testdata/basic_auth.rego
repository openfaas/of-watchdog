package api.authz.basic

default allow = false

allow {
	input.path = "/api/private"
	valid_basic_auth
}

valid_basic_auth {
	value := trim_prefix(input.authorization, "Basic ")
	decoded := base64.decode(value)

	print(decoded)

	colon := indexof(decoded, ":")
	username := substring(decoded, 0, colon)
	password := substring(decoded, colon + 1, -1)

	print(username)
	print(password)

	credentials[username] == password
}

credentials := {"bob": "secretvalue"}
