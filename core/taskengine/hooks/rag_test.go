package hooks_test

import (
	"log"
	"strings"
	"testing"
	"time"

	"github.com/contenox/contenox/core/indexrepo"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/vectors"
	"github.com/contenox/contenox/core/services/testingsetup"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/contenox/contenox/core/taskengine/hooks"
	"github.com/stretchr/testify/require"
)

func TestSystemRag(t *testing.T) {
	config := &serverops.Config{
		JWTExpiry:  "1h",
		EmbedModel: "nomic-embed-text:latest",
	}
	testenv := testingsetup.New(t.Context(), serverops.NoopTracker{}).
		WithTriggerChan().
		WithServiceManager(config).
		WithDBConn("test").
		WithDBManager().
		WithPubSub().
		WithOllama().
		WithState().
		WithBackend().
		RunState().
		RunDownloadManager().
		WithDefaultUser().
		Build()
	defer testenv.Cleanup()
	if testenv.Err != nil {
		t.Fatal(testenv.Err)
	}
	require.NoError(t, testenv.WaitForModel(config.EmbedModel).Err)

	embedder, err := testenv.NewEmbedder(config)
	if err != nil {
		log.Fatalf("initializing embedding pool failed: %v", err)
	}

	vectorStore, cleanupVectorStore, err := vectors.New(t.Context(), config.VectorStoreURL, vectors.Args{
		Timeout: 1 * time.Second,
		SearchArgs: vectors.SearchArgs{
			Epsilon: 0.9,
			Radius:  20.0,
		},
	})
	if err != nil {
		log.Fatalf("initializing vector store failed: %v", err)
	}
	defer cleanupVectorStore()
	dbInstance := testenv.GetDBInstance()
	ragHook := hooks.NewRagHook(embedder, vectorStore, dbInstance)
	supports, err := ragHook.Supports(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(supports) != 1 {
		t.Fatal("registry returns wrong number of hooks")
	}
	if supports[0] != "rag" {
		t.Fatal("registry returns wrong hook name")
	}
	// populate the vector store
	ingestText := func(resourceId, text string) {
		chunks := strings.Split(text, "\n\n")
		_, _, err := indexrepo.IngestChunks(
			t.Context(),
			embedder,
			vectorStore,
			dbInstance.WithoutTransaction(),
			resourceId,
			"file",
			chunks,
			indexrepo.DummyaugmentStrategy,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	textDataBase := map[string]string{
		"1": "Machine learning is a subset of artificial intelligence that focuses on building systems that learn from data. It involves algorithms that improve automatically through experience over time. Common approaches include supervised learning, where models are trained on labeled data, and unsupervised learning, which finds hidden patterns in unlabeled data.\n\nApplications of machine learning are vast, ranging from image recognition and natural language processing to recommendation systems and fraud detection. As data availability increases, machine learning continues to drive innovation across industries such as healthcare, finance, and autonomous vehicles.",

		"2": "Blockchain is a decentralized digital ledger that records transactions across a network of computers. Each block contains a list of transactions and is linked to the previous one using cryptographic techniques, ensuring data integrity and security. Originally developed for cryptocurrencies like Bitcoin, blockchain technology now finds applications in supply chain management, voting systems, and smart contracts.\n\nThe core principles of blockchain include decentralization, immutability, and transparency. By eliminating central authorities, blockchain enables peer-to-peer transactions and reduces the risk of tampering. However, scalability and energy consumption remain significant challenges for widespread adoption.",

		"3": "Quantum computing leverages the principles of quantum mechanics to perform computations that classical computers cannot efficiently achieve. Qubits, the basic units of quantum information, can exist in superpositions of states, enabling parallel processing capabilities. This technology holds promise for solving complex problems in cryptography, optimization, and material science.\n\nDespite ongoing advancements, quantum computing faces challenges such as qubit stability, error correction, and the need for extremely low temperatures. Companies like IBM and Google are actively researching quantum processors to achieve practical quantum advantage in the near future.",

		"4": "Cloud computing offers scalable IT resources over the internet, eliminating the need for local servers and infrastructure. It encompasses three primary service models: Infrastructure as a Service (IaaS), Platform as a Service (PaaS), and Software as a Service (SaaS). Each model provides varying levels of control, flexibility, and management responsibilities.\n\nIaaS provides virtualized computing resources, PaaS offers development platforms with tools and libraries pre-configured, while SaaS delivers software applications directly via the web. Choosing the appropriate model depends on an organization's requirements for customization, cost, and operational complexity.",

		"5": "Cybersecurity involves protecting computer systems, networks, and data from digital threats and unauthorized access. Key best practices include implementing strong password policies, regular software updates, and multi-factor authentication. Encryption of sensitive data ensures confidentiality during transmission and storage.\n\nOrganizations should conduct routine security audits, employee training programs, and maintain robust firewalls and intrusion detection systems. With the evolving threat landscape, proactive measures such as zero-trust architecture and incident response planning are essential for mitigating cyber risks.",

		"6": "The Internet of Things refers to the network of interconnected devices embedded with sensors, software, and connectivity to exchange data. These devices range from smart home appliances and wearable fitness trackers to industrial machinery and autonomous vehicles. IoT enables real-time monitoring, automation, and data-driven decision-making across various domains.\n\nWhile IoT enhances convenience and efficiency, it also raises concerns about privacy, device vulnerabilities, and network security. Standardization and edge computing solutions are critical for managing the vast amounts of data generated by IoT ecosystems effectively.",

		"7": "Artificial intelligence ethics addresses the moral implications and societal impacts of AI technologies. Issues such as algorithmic bias, transparency of decision-making processes, and job displacement require careful consideration. Ensuring fairness, accountability, and inclusivity in AI systems is crucial for building public trust.\n\nGovernments and organizations are developing regulatory frameworks and ethical guidelines to govern AI development. Balancing innovation with human rights protection and environmental sustainability remains a complex challenge in the rapidly advancing AI landscape.",

		"8": "Big data refers to extremely large datasets that cannot be processed using traditional data processing tools. Technologies like Hadoop, Apache Spark, and NoSQL databases enable efficient storage, processing, and analysis of big data. These frameworks support distributed computing and real-time analytics for structured and unstructured data.\n\nData lakes, machine learning pipelines, and visualization tools play a vital role in extracting insights from big data. Industries leverage big data analytics for customer behavior analysis, predictive maintenance, and optimizing business operations, driving competitive advantage.",

		"9": "DevOps is a set of practices that combines software development (Dev) and IT operations (Ops) to shorten the development lifecycle and deliver high-quality software continuously. It emphasizes automation, collaboration, and monitoring throughout the application lifecycle. Tools like Jenkins, Docker, and Kubernetes facilitate CI/CD pipelines and container orchestration.\n\nBy fostering a culture of shared responsibility and agile workflows, DevOps improves deployment frequency, system reliability, and team productivity. Monitoring and feedback loops ensure rapid issue resolution and alignment with business objectives.",

		"10": "Renewable energy sources such as solar, wind, and hydropower provide sustainable alternatives to fossil fuels. Advances in photovoltaic cells, wind turbine efficiency, and battery storage are making renewables more accessible and cost-effective. Green hydrogen and tidal energy are emerging areas with significant potential for decarbonizing energy systems.\n\nEnergy storage solutions like lithium-ion batteries and grid modernization are critical for addressing intermittency challenges. Governments and private sectors worldwide are investing in renewable infrastructure to combat climate change and achieve energy independence.",
	}

	for id, text := range textDataBase {
		ingestText(id, text)
	}

	t.Run("ragHook", func(t *testing.T) {
		// Define a test query that should match the "machine learning" document
		query := "What is machine learning and how does it work?"

		// Create a HookCall with the "rag" hook and configure top_k
		hookCall := &taskengine.HookCall{
			Type: "rag",
			Args: map[string]string{
				"top_k":   "1",
				"epsilon": "0.1",
				"radius":  "25",
			},
		}

		// Execute the RAG hook with the query
		status, result, dataType, err := ragHook.Exec(
			t.Context(),
			query,
			taskengine.DataTypeString,
			hookCall,
		)
		// Assert no errors occurred
		if err != nil {
			t.Fatalf("RAG hook execution failed: %v", err)
		}

		// Assert successful execution status
		if status != taskengine.StatusSuccess {
			t.Fatalf("Expected StatusSuccess, got %d", status)
		}

		// Assert correct data type
		if dataType != taskengine.DataTypeSearchResults {
			t.Fatalf("Expected DataTypeSearchResults, got %v", dataType)
		}

		// Type assert the result
		results, ok := result.([]taskengine.SearchResults)
		if !ok {
			t.Fatal("Expected result to be a slice of SearchResults")
		}

		// Assert we received at least one result
		if len(results) == 0 {
			t.Fatal("Expected at least one result, got none")
		}

		// Assert the most relevant result is document #1
		if results[0].ID != "1" {
			t.Errorf("Expected top result ID to be '1', got '%s'", results[0].ID)
		}

		if results[0].Distance > 0.2 {
			t.Logf("Warning: Top result distance is %f, which might be too high", results[0].Distance)
		}
	})
}
