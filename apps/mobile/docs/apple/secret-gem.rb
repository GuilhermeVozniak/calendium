require "jwt"

key_file = File.expand_path("AuthKey_8MX6Q9WW35.p8", __dir__)
team_id = "CT22R575UG"
client_id = "com.calendium.app.service"
key_id = "8MX6Q9WW35"
validity_period = 180 # In days. Max 180 (6 months) according to Apple docs.

private_key = OpenSSL::PKey::EC.new IO.read key_file

token = JWT.encode(
	{
		iss: team_id,
		iat: Time.now.to_i,
		exp: Time.now.to_i + 86400 * validity_period,
		aud: "https://appleid.apple.com",
		sub: client_id
	},
	private_key,
	"ES256",
	header_fields=
	{
		kid: key_id 
	}
)
puts token