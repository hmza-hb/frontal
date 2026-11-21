const noComments = {
  meta: {
    type: "problem",
    schema: [],
    messages: {
      comment: "Comments are not allowed.",
    },
  },
  create(context) {
    return {
      Program() {
        for (const comment of context.sourceCode.getAllComments()) {
          context.report({
            loc: comment.loc,
            messageId: "comment",
          });
        }
      },
    };
  },
};

module.exports = {
  rules: {
    "no-comments": noComments,
  },
};