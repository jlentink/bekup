// @ts-check
/**
 * Local ESLint rule (D-235): user-visible text comes from the message tables
 * (`defineMessages({ en, nl })`), never from literals in the markup, so both
 * languages stay complete. Flags letters in JSX text and in the literal
 * parts (strings, template text, both sides of conditionals, `&&`, `+`) of
 * JSX children, text-carrying attributes and toast() messages/descriptions.
 * Not covered (reviewed by hand, and by the parity test for tables): plain
 * `.ts` modules, e.g. zod messages; props outside TEXT_ATTRIBUTES.
 */

/** Attributes whose value a user reads or hears. */
const TEXT_ATTRIBUTES = new Set([
  "aria-label",
  "aria-description",
  "aria-roledescription",
  "aria-valuetext",
  "alt",
  "title",
  "placeholder",
  "label",
  "description",
  "lead",
  "eyebrow",
  "heading",
  "confirmLabel",
  "cancelLabel",
  "hint",
  "emptyText",
  "emptyLabel",
  "subtitle",
  "message",
  "tooltip",
  "helperText",
  "caption",
]);

/** Literal text that is the same in every language. */
const ALLOWED = new Set(["bekup"]);

const LETTER = /\p{L}/u;

/** @param {string} text */
function isUserText(text) {
  const t = text.trim();
  return t !== "" && LETTER.test(t) && !ALLOWED.has(t);
}

/**
 * The literal text parts of an expression: string literals, the fixed
 * parts of template literals, and both sides of conditionals, `&&`/`||`/`??`
 * and `+`. Anything else (identifiers, calls, members) yields nothing.
 * @param {any} node
 * @returns {string[]}
 */
function literalTexts(node) {
  if (!node) return [];
  switch (node.type) {
    case "Literal":
      return typeof node.value === "string" ? [node.value] : [];
    case "TemplateLiteral":
      return node.quasis.map((/** @type {any} */ q) => q.value.cooked ?? "");
    case "JSXExpressionContainer":
      return literalTexts(node.expression);
    case "ConditionalExpression":
      return [...literalTexts(node.consequent), ...literalTexts(node.alternate)];
    case "LogicalExpression":
      return [...literalTexts(node.left), ...literalTexts(node.right)];
    case "BinaryExpression":
      return node.operator === "+" ? [...literalTexts(node.left), ...literalTexts(node.right)] : [];
    case "TSAsExpression":
    case "TSSatisfiesExpression":
      return literalTexts(node.expression);
    default:
      return [];
  }
}

/** @param {any} node */
function hasUserText(node) {
  return literalTexts(node).some(isUserText);
}

/** @param {any} callee */
function isToast(callee) {
  if (callee.type === "Identifier") return callee.name === "toast";
  return (
    callee.type === "MemberExpression" &&
    callee.object.type === "Identifier" &&
    callee.object.name === "toast"
  );
}

/** @type {import("eslint").Rule.RuleModule} */
const rule = {
  meta: {
    type: "problem",
    docs: { description: "User-visible text must come from a message table (D-235)." },
    schema: [],
    messages: {
      text: "Literal UI text: move it into the component's message table (defineMessages, en + nl).",
    },
  },
  create(context) {
    return {
      /** @param {any} node */
      JSXText(node) {
        if (isUserText(node.value)) context.report({ node, messageId: "text" });
      },
      /** @param {any} node */
      JSXAttribute(node) {
        if (node.name.type !== "JSXIdentifier" || !TEXT_ATTRIBUTES.has(node.name.name)) return;
        if (hasUserText(node.value)) context.report({ node, messageId: "text" });
      },
      /** @param {any} node */
      JSXExpressionContainer(node) {
        // {"Literal"}, {ok ? "Yes" : "No"}, {`n items`} as a child are JSX text too.
        if (node.parent?.type !== "JSXElement" && node.parent?.type !== "JSXFragment") return;
        if (hasUserText(node.expression)) context.report({ node, messageId: "text" });
      },
      /** @param {any} node */
      CallExpression(node) {
        if (!isToast(node.callee)) return;
        const [message, options] = node.arguments;
        let bad = hasUserText(message);
        if (options?.type === "ObjectExpression") {
          for (const prop of options.properties) {
            if (
              prop.type === "Property" &&
              prop.key.type === "Identifier" &&
              prop.key.name === "description" &&
              hasUserText(prop.value)
            )
              bad = true;
          }
        }
        if (bad) context.report({ node, messageId: "text" });
      },
    };
  },
};

export default { rules: { "no-ui-literals": rule } };
