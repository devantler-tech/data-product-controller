import com.fasterxml.jackson.databind.ObjectMapper;
import jakarta.json.Json;
import jakarta.json.JsonArray;
import jakarta.json.JsonObject;
import jakarta.json.JsonValue;
import java.io.ByteArrayInputStream;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.eclipse.edc.connector.controlplane.catalog.spi.Catalog;
import org.eclipse.edc.connector.controlplane.catalog.spi.Distribution;
import org.eclipse.edc.participant.spi.ParticipantIdMapper;
import org.eclipse.edc.connector.controlplane.transform.odrl.OdrlTransformersFactory;
import org.eclipse.edc.json.JacksonTypeManager;
import org.eclipse.edc.jsonld.JsonLdConfiguration;
import org.eclipse.edc.jsonld.TitaniumJsonLd;
import org.eclipse.edc.jsonld.util.JacksonJsonLd;
import org.eclipse.edc.policy.model.AtomicConstraint;
import org.eclipse.edc.policy.model.LiteralExpression;
import org.eclipse.edc.policy.model.Policy;
import org.eclipse.edc.policy.model.Permission;
import org.eclipse.edc.policy.model.Prohibition;
import org.eclipse.edc.policy.model.Duty;
import org.eclipse.edc.policy.model.Rule;
import org.eclipse.edc.protocol.dsp.catalog.transform.to.JsonObjectToDataServiceTransformer;
import org.eclipse.edc.protocol.dsp.catalog.transform.to.JsonObjectToDatasetTransformer;
import org.eclipse.edc.protocol.dsp.catalog.transform.to.JsonObjectToDistributionTransformer;
import org.eclipse.edc.protocol.dsp.spi.type.Dsp2025Constants;
import org.eclipse.edc.spi.constants.CoreConstants;
import org.eclipse.edc.spi.monitor.ConsoleMonitor;
import org.eclipse.edc.transform.TypeTransformerRegistryImpl;
import org.eclipse.edc.transform.transformer.edc.to.JsonValueToGenericTypeTransformer;

/**
 * Independent acceptance fixture using published EDC classes, never a product runtime.
 * Output is an observed projection, not a negotiation, transfer or compatibility approval.
 */
public final class EdcImport {
    private static final String CONTEXT = "https://w3id.org/dspace/2025/1/context.jsonld";
    private static final String ODRL = "https://w3id.org/dspace/2025/1/odrl-profile.jsonld";
    private static final ObjectMapper JSON = new ObjectMapper()
            .enable(com.fasterxml.jackson.core.JsonParser.Feature.STRICT_DUPLICATE_DETECTION)
            .enable(com.fasterxml.jackson.databind.DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
            .enable(com.fasterxml.jackson.databind.SerializationFeature.ORDER_MAP_ENTRIES_BY_KEYS);

    static {
        JSON.getFactory().setStreamReadConstraints(com.fasterxml.jackson.core.StreamReadConstraints.builder()
                .maxNestingDepth(32).maxStringLength(16384).maxNumberLength(128).build());
    }

    /** This command has no instance state or product-runtime entrypoint. */
    private EdcImport() {}

    /** Emit one receipt only after expansion, typed import and the independent oracle agree. */
    public static void main(String[] args) {
        String stage = "input";
        try {
            if (args.length != 3) {
                throw new IllegalArgumentException("expected export, context directory and oracle");
            }
            byte[] input = boundedFile(Path.of(args[0]), 2 * 1024 * 1024);
            if (!JSON.readTree(input).isObject()) {
                throw new IllegalArgumentException("expected one complete JSON object");
            }
            JsonObject document;
            try (var reader = Json.createReader(new ByteArrayInputStream(input))) {
                document = reader.readObject();
            }
            if (!document.getJsonArray("@context").equals(
                    Json.createArrayBuilder().add(CONTEXT).build())) {
                throw new IllegalArgumentException("unregistered context");
            }
            contexts(document, 0);
            stage = "profile";
            profile(document);
            Path contextDir = Path.of(args[1]);
            Path dspContext = pinnedContext(contextDir, "context.jsonld",
                    "4c09141559c51331c8cb6946c93040d799c39f269afde5d76d79076b9645e1ea");
            Path odrlContext = pinnedContext(contextDir, "odrl-profile.jsonld",
                    "65b96c89a93830c6d8c1b7bba93f4ce64411fe9bb942b445a6d27862511bbb9d");

            // EDC's console monitor uses stdout; reserve stdout for one complete receipt.
            var output = System.out;
            System.setOut(System.err);
            var monitor = new ConsoleMonitor();
            var jsonLd = new TitaniumJsonLd(monitor, JsonLdConfiguration.Builder.newInstance()
                    .httpEnabled(false).httpsEnabled(false).build());
            jsonLd.registerCachedDocument(CONTEXT, dspContext.toUri());
            jsonLd.registerCachedDocument(ODRL, odrlContext.toUri());
            var types = new JacksonTypeManager();
            types.registerContext(CoreConstants.JSON_LD, JacksonJsonLd.createObjectMapper());
            var registry = new TypeTransformerRegistryImpl(monitor);
            registry.register(new JsonValueToGenericTypeTransformer(types, CoreConstants.JSON_LD));
            ParticipantIdMapper identity = new ParticipantIdMapper() {
                /** Preserve the export's declared participant IRI without inventing a resolver. */
                @Override
                public String toIri(String id) {
                    return id;
                }

                /** Preserve the imported IRI; this fixture does not establish native participant identity. */
                @Override
                public String fromIri(String id) {
                    return id;
                }
            };
            OdrlTransformersFactory.jsonObjectToOdrlTransformers(identity).forEach(registry::register);
            var dsp = registry.forContext(Dsp2025Constants.DSP_TRANSFORMER_CONTEXT_V_2025_1);
            dsp.register(new org.eclipse.edc.protocol.dsp.catalog.transform.v2025.to.JsonObjectToCatalogTransformer());
            dsp.register(new JsonObjectToDatasetTransformer());
            dsp.register(new JsonObjectToDistributionTransformer());
            dsp.register(new JsonObjectToDataServiceTransformer());
            stage = "expansion";
            var expanded = jsonLd.expand(document)
                    .orElseThrow(f -> new IllegalArgumentException("JSON-LD expansion failed"));
            stage = "transformation";
            var catalog = dsp.transform(expanded, Catalog.class)
                    .orElseThrow(f -> new IllegalArgumentException("EDC transformation failed"));
            stage = "oracle";
            var receipt = projection(catalog, expanded);
            JsonObject observed;
            JsonObject expected;
            try (var reader = Json.createReader(new ByteArrayInputStream(JSON.writeValueAsBytes(receipt)))) {
                observed = reader.readObject();
            }
            byte[] oracle = boundedFile(Path.of(args[2]), 2 * 1024 * 1024);
            if (!JSON.readTree(oracle).isObject()) {
                throw new IllegalArgumentException("expected one complete oracle");
            }
            try (var reader = Json.createReader(new ByteArrayInputStream(oracle))) {
                expected = reader.readObject();
            }
            if (!observed.equals(expected)) {
                throw new IllegalArgumentException("consumer projection differs from independent oracle");
            }
            receipt.put("inputSha256", sha256(input));
            String encoded = JSON.writeValueAsString(receipt);
            if (encoded.length() > 2 * 1024 * 1024) {
                throw new IllegalArgumentException("receipt exceeds bound");
            }
            output.println(encoded);
            if (output.checkError()) {
                throw new java.io.IOException("receipt output failed");
            }
        } catch (Exception | StackOverflowError e) {
            System.err.println("EDC import rejected (" + stage + "): " + e.getClass().getSimpleName());
            System.exit(1);
        }
    }

    /** Admit only the exporter's closed profile before a typed consumer can discard information. */
    private static void profile(JsonObject document) {
        fields(document, Set.of("@context", "@id", "@type", "participantId", "service", "dataset"),
                Set.of("@context", "service", "dataset"));
        for (var value : document.getJsonArray("service")) {
            service(value.asJsonObject());
        }
        for (var value : document.getJsonArray("dataset")) {
            var dataset = value.asJsonObject();
            fields(dataset, Set.of("@id", "@type", "dct:title", "dct:description", "dcat:version",
                    "hasPolicy", "distribution"), Set.of("hasPolicy", "distribution"));
            var offers = new HashSet<String>();
            for (var item : dataset.getJsonArray("hasPolicy")) {
                var offer = item.asJsonObject();
                fields(offer, Set.of("@id", "@type", "assigner", "permission", "prohibition", "obligation"),
                        Set.of("permission", "prohibition", "obligation"));
                if (!offers.add(offer.getString("@id"))) {
                    throw new IllegalArgumentException("duplicate offer identity");
                }
                for (var category : List.of("permission", "prohibition", "obligation")) {
                    if (!offer.containsKey(category)) {
                        continue;
                    }
                    for (var ruleValue : offer.getJsonArray(category)) {
                        var rule = ruleValue.asJsonObject();
                        fields(rule, Set.of("action", "constraint"), Set.of("constraint"));
                        if (rule.containsKey("constraint")) {
                            for (var constraint : rule.getJsonArray("constraint")) {
                                fields(constraint.asJsonObject(), Set.of("leftOperand", "operator", "rightOperand"),
                                        Set.of());
                            }
                        }
                    }
                }
            }
            for (var item : dataset.getJsonArray("distribution")) {
                var distribution = item.asJsonObject();
                fields(distribution, Set.of("@id", "@type", "format", "accessService"), Set.of("accessService"));
                if (distribution.containsKey("accessService")) {
                    service(distribution.getJsonObject("accessService"));
                }
            }
        }
    }

    /** Endpoint arrays and extension keywords cannot be silently reduced to their first value. */
    private static void service(JsonObject service) {
        fields(service, Set.of("@id", "@type", "endpointURL"), Set.of());
    }

    /** Unknown fields and containers for scalar predicates are outside this acceptance profile. */
    private static void fields(JsonObject object, Set<String> allowed, Set<String> containers) {
        for (var entry : object.entrySet()) {
            if (!allowed.contains(entry.getKey()) || (!containers.contains(entry.getKey())
                    && (entry.getValue() instanceof JsonArray || entry.getValue() instanceof JsonObject))) {
                throw new IllegalArgumentException("unsupported or ambiguous profile field");
            }
        }
    }

    /** Reject nested or foreign context loading before any EDC document loader runs. */
    private static void contexts(JsonValue value, int depth) {
        if (depth > 32) {
            throw new IllegalArgumentException("JSON nesting exceeds bound");
        }
        if (value instanceof JsonObject object) {
            for (var entry : object.entrySet()) {
                if (entry.getKey().equals("@context") && depth != 0) {
                    throw new IllegalArgumentException("nested context");
                }
                contexts(entry.getValue(), depth + 1);
            }
        } else if (value instanceof JsonArray array) {
            array.forEach(item -> contexts(item, depth + 1));
        }
    }

    /** Read only a bounded regular file; fixture inputs do not need symlink support. */
    private static byte[] boundedFile(Path path, int limit) throws Exception {
        if (!Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS) || Files.size(path) > limit) {
            throw new IllegalArgumentException("input unavailable or exceeds bound");
        }
        try (var stream = Files.newInputStream(path)) {
            byte[] bytes = stream.readNBytes(limit + 1);
            if (bytes.length > limit) {
                throw new IllegalArgumentException("input exceeds bound");
            }
            return bytes;
        }
    }

    /** Bind every cached context to the repository's unmodified official fixture. */
    private static Path pinnedContext(Path directory, String name, String hash) throws Exception {
        Path path = directory.resolve(name);
        if (!sha256(boundedFile(path, 256 * 1024)).equals(hash)) {
            throw new IllegalArgumentException("context hash mismatch");
        }
        return path;
    }

    /** Hash the exact bytes consumed, including their original trailing newline. */
    private static String sha256(byte[] value) throws Exception {
        return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(value));
    }

    /** Compare explicit model getters: DataService.equals dereferences nullable terms. */
    private static Map<String, Object> projection(Catalog catalog, JsonObject expanded) throws Exception {
        var result = new LinkedHashMap<String, Object>();
        result.put("catalog", catalog.getId());
        result.put("participant", catalog.getParticipantId());
        var services = new ArrayList<Map<String, Object>>();
        for (var service : catalog.getDataServices()) {
            services.add(Map.of("id", service.getId(), "endpoint", service.getEndpointUrl()));
        }
        result.put("services", sorted(services));
        var datasets = new ArrayList<Map<String, Object>>();
        for (var dataset : catalog.getDatasets()) {
            var data = new LinkedHashMap<String, Object>();
            data.put("id", dataset.getId());
            data.put("properties", dataset.getProperties());
            var offers = new ArrayList<Map<String, Object>>();
            for (var offer : dataset.getOffers().entrySet()) {
                var policy = policy(offer.getValue());
                policy.put("id", offer.getKey());
                offers.add(policy);
            }
            data.put("offers", sorted(offers));
            var distributions = new ArrayList<Map<String, Object>>();
            for (var distribution : dataset.getDistributions()) {
                distributions.add(Map.of("format", distribution.getFormat(),
                        "service", distribution.getDataService().getId()));
            }
            // Preserve multiplicity: equal signatures must never collapse distinct outputs.
            data.put("distributions", sorted(distributions));
            datasets.add(data);
        }
        result.put("datasets", sorted(datasets));
        var links = distributionLinks(expanded);
        result.put("expandedDistributions", sorted(links));
        var identities = links.stream().map(link -> (String) link.get("id")).sorted().toList();
        result.put("expandedDistributionIds", identities);
        for (var method : Distribution.class.getMethods()) {
            if (method.getName().equals("getId")) {
                throw new IllegalArgumentException("consumer identity model changed; reassess fidelity");
            }
        }
        result.put("compatibility", identities.isEmpty() ? "no-distributions" : "incompatible");
        result.put("losses", identities.isEmpty() ? List.of() : List.of("typed-distribution-id"));
        return result;
    }

    /** Keep offer keys outside the policy while retaining all supported policy structure. */
    private static Map<String, Object> policy(Policy policy) throws Exception {
        var result = new LinkedHashMap<String, Object>();
        result.put("type", policy.getType().name());
        result.put("assigner", policy.getAssigner());
        result.put("assignee", policy.getAssignee());
        result.put("target", policy.getTarget());
        result.put("profiles", policy.getProfiles());
        result.put("inheritsFrom", policy.getInheritsFrom());
        result.put("extensions", policy.getExtensibleProperties());
        result.put("permission", rules(policy.getPermissions()));
        result.put("prohibition", rules(policy.getProhibitions()));
        result.put("obligation", rules(policy.getObligations()));
        return result;
    }

    /** Preserve scalar types, rule categories and unexpected nested-rule structure. */
    private static List<Map<String, Object>> rules(List<? extends Rule> rules) throws Exception {
        var result = new ArrayList<Map<String, Object>>();
        for (var rule : rules) {
            var projected = new LinkedHashMap<String, Object>();
            projected.put("action", rule.getAction().getType());
            projected.put("includedIn", rule.getAction().getIncludedIn());
            projected.put("actionConstraint", rule.getAction().getConstraint());
            List<? extends Rule> nested = List.of();
            if (rule instanceof Permission permission) {
                nested = permission.getDuties();
            } else if (rule instanceof Prohibition prohibition) {
                nested = prohibition.getRemedies();
            } else if (rule instanceof Duty duty) {
                nested = duty.getConsequences();
            }
            projected.put("nested", rules(nested));
            var constraints = new ArrayList<Map<String, Object>>();
            for (var constraint : rule.getConstraints()) {
                if (!(constraint instanceof AtomicConstraint atomic)
                        || !(atomic.getLeftExpression() instanceof LiteralExpression left)
                        || !(atomic.getRightExpression() instanceof LiteralExpression right)) {
                    throw new IllegalArgumentException("unexpected constraint model");
                }
                constraints.add(Map.of("left", left.getValue(),
                        "operator", atomic.getOperator().getOdrlRepresentation(),
                        "right", right.getValue()));
            }
            projected.put("constraints", sorted(constraints));
            result.add(projected);
        }
        return sorted(result);
    }

    /** Sort semantic arrays without collapsing duplicate output signatures. */
    private static List<Map<String, Object>> sorted(List<Map<String, Object>> values) throws Exception {
        values.sort(Comparator.comparing(value -> {
            try {
                return JSON.writeValueAsString(value);
            } catch (Exception e) {
                throw new IllegalArgumentException("projection cannot be ordered");
            }
        }));
        return values;
    }

    /** Bind each expanded output to its dataset, format, service and inline endpoint. */
    private static List<Map<String, Object>> distributionLinks(JsonObject expanded) {
        var links = new ArrayList<Map<String, Object>>();
        for (var value : expanded.getJsonArray("http://www.w3.org/ns/dcat#dataset")) {
            var dataset = value.asJsonObject();
            for (var item : dataset.getJsonArray("http://www.w3.org/ns/dcat#distribution")) {
                var distribution = item.asJsonObject();
                var services = distribution.getJsonArray("http://www.w3.org/ns/dcat#accessService");
                if (services.size() != 1) {
                    throw new IllegalArgumentException("ambiguous distribution service");
                }
                var service = services.getJsonObject(0);
                links.add(Map.of("dataset", dataset.getString("@id"),
                        "id", distribution.getString("@id"),
                        "format", iri(distribution, "http://purl.org/dc/terms/format"),
                        "service", service.getString("@id"),
                        "endpoint", singular(service, "http://www.w3.org/ns/dcat#endpointURL", "@value")));
            }
        }
        return links;
    }

    /** Reject a lossy first-value selection when a supposedly singular link is ambiguous. */
    private static String iri(JsonObject object, String property) {
        return singular(object, property, "@id");
    }

    /** DSP represents the endpoint as a literal and format as an IRI; retain that distinction. */
    private static String singular(JsonObject object, String property, String key) {
        var values = object.getJsonArray(property);
        if (values == null || values.size() != 1) {
            throw new IllegalArgumentException("ambiguous expanded value");
        }
        return values.getJsonObject(0).getString(key);
    }
}
