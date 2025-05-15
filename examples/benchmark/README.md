
Some examples:

    # Test ECDSA signing with 25 concurrent clients and n=3, t=1 
    go run . -operation sign -ecdsaClients 50 -duration 30s -threshold 1 -signers 3 -node http://apikey0@localhost:80/tsm0 -node http://apikey1@localhost:80/tsm1 -node http://apikey2@localhost:80/tsm2

    # Test Ed25519 signing with 25 concurrent clients and n=3, t=2
    go run . -operation sign -ed25519Clients 25 -duration 30s -threshold 2 -signers 3 -node http://apikey0@localhost:80/tsm0 -node http://apikey1@localhost:80/tsm1 -node http://apikey2@localhost:80/tsm2

    # Test ECDSA presig generation with 3 concurrent clients; each client generates a total of 1000 presigs in batches of size 25
    # Clients stop when done, or aftr 10s.
    # Each client stores the generated presig IDs in a file in the ./presigs dir, for later use.
    go run . -operation presigGen -ecdsaClients 3 -duration 10s -threshold 2 -signers 3 --presigCount=1000 --presigBatchSize=25 -node http://apikey@localhost:8080 -node http://apikey@localhost:8081 -node http://apikey@localhost:8082 

    # Test ECDSA online signing with 3 concurrent clients.
    # Each client reads presig IDs stored in the ./presigs dir and uses these for online signing.
    # Each client stop when all the presignature IDs have been used, or after 10s 
    go run . -operation onlineSign -ecdsaClients 3 -duration 10s -threshold 2 -signers 3 -node http://apikey0@localhost:80/tsm0 -node http://apikey1@localhost:80/tsm1 -node http://apikey2@localhost:80/tsm2
