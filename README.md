# Argon2 Go

This is a fork of the standard library `x/crypto/argon2`, exposing additional Argon2 parameters. It
implements the key derivation function Argon2. Argon2 was selected as the winner of the Password
Hashing Competition and can be used to derive cryptographic keys from passwords.

```go
argon2.DeriveKey(mode, password, salt, secret, data, time, memory, threads, keyLen)
```

Additional parameters supported in this fork:

- `mode`: one of Argon2d, Argon2i, or Argon2id
- `secret`: commonly used to include a secret key or pepper in the hash; the secret is accessed at
  the application level, not stored in the database alongside the salt; an attacker who only has
  access to the database cannot crack any hashed passwords
- `data`: extra data to be fed into the hash
