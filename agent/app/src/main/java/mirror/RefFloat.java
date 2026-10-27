package mirror;

import java.lang.reflect.Field;

/* loaded from: classes2.dex */
public class RefFloat {
    private Field field;

    public RefFloat(Class cls, Field field) throws NoSuchFieldException {
        Field declaredField = cls.getDeclaredField(field.getName());
        this.field = declaredField;
        declaredField.setAccessible(true);
    }

    public float get(Object object) {
        try {
            return this.field.getFloat(object);
        } catch (Exception e) {
            return 0.0f;
        }
    }

    public void set(Object obj, float value) throws IllegalAccessException, IllegalArgumentException {
        try {
            this.field.setFloat(obj, value);
        } catch (Exception e) {
        }
    }
}
