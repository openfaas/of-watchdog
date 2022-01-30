package api.authz

default allow = false

allow {
	input.method == "GET"
	input.path == "/api/public"
}

allow {
	is_admin
}

is_admin {
	input.authorization == "admin"
}

allow {
	input.rawBody == "permitted"
}

allow {
	input.body.override == "permitted"
}
