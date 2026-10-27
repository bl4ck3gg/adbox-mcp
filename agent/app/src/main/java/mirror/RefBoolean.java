package mirror;

import java.lang.reflect.Field;

/* loaded from: classes2.dex */
public class RefBoolean {
    private Field field;

    public RefBoolean(Class<?> cls, Field field) throws NoSuchFieldException {
        Field declaredField = cls.getDeclaredField(field.getName());
        this.field = declaredField;
        declaredField.setAccessible(true);
    }

    public boolean get(Object object) {
        try {
            return this.field.getBoolean(object);
        } catch (Exception e) {
            return false;
        }
    }

    public void set(Object obj, boolean value) throws IllegalAccessException, IllegalArgumentException {
        try {
            this.field.setBoolean(obj, value);
        } catch (Exception e) {
        }
    }
}
