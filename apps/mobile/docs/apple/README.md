## Apple Secret Gem Script

After downloading the AuthKey file from the Apple Developer Console, run this script:

### Prerequisites

* The ruby gem needs https://github.com/jwt/ruby-jwt
* Install with `sudo gem install jwt`
* Reference video: https://www.youtube.com/watch?v=6I2JEky20ME&t=401s

### Configuration

The configuration looks like this:

```ruby
key_file = "/Users/guilherme/Dev/pessoal/calendium/docs/apple/AuthKey_8MX6Q9WW35.p8"
team_id = "CT22R575UG"
client_id = "com.calendium.app.service"
key_id = "8MX6Q9WW35"
```

### Running the script

Run the script with `ruby secret-gem.rb`.